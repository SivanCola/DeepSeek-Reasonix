package sessionv3

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"reasonix/internal/provider"
)

// Session is the in-memory typed event log and its projections. It owns every
// piece of business state for one session identity: committed batches, the
// operation-idempotency table, sequence allocation, and the projection. The
// physical SessionHandle behind PersistenceBinding owns only bytes, the writer
// lease, and durability progress.
//
// A commit becomes observable here before it is durable. That is deliberate and
// mirrors DSH: accepting an event updates the projection and the UI, while the
// binding batches the write-behind and Flush marks a semantic checkpoint.
type Session struct {
	mu          sync.Mutex
	id          string
	manifest    Manifest
	next        uint64
	commits     []Commit
	operations  map[string]operationRecord
	projection  Projection
	binding     *PersistenceBinding
	sealed      bool
	sealedError error
	readOnly    bool
	// coldHandle backs a read-only session, which has no binding because it
	// never enqueues or drains anything.
	coldHandle SessionHandle
}

// PreparedBatch is a validated, self-contained commit payload. Every expensive
// or fallible step — payload copying, schema validation, turn identity
// derivation, and the operation hash — happens here, before the caller takes
// the activity commit gate. CommitPrepared then only assigns identity and
// extends the log.
type PreparedBatch struct {
	operationID string
	turnID      string
	events      []Event
	hash        string
}

// OperationID reports the stable idempotency key of the prepared batch.
func (p PreparedBatch) OperationID() string { return p.operationID }

// Empty reports whether the batch carries no committable event.
func (p PreparedBatch) Empty() bool { return len(p.events) == 0 }

// newSession builds the live in-memory session over an already-open binding.
// commits is the durable prefix replayed by the handle; the projection is
// rebuilt from it so no business state is inherited from the physical layer.
func newSession(id string, manifest Manifest, commits []Commit, projection Projection, binding *PersistenceBinding) *Session {
	operations := make(map[string]operationRecord, len(commits))
	next := uint64(1)
	for _, commit := range commits {
		operations[commit.OperationID] = operationRecord{hash: commit.OperationHash, commit: commit}
		next = commit.LastSequence() + 1
	}
	return &Session{
		id: id, manifest: manifest, next: next, commits: cloneCommits(commits),
		operations: operations, projection: projection, binding: binding,
	}
}

// ID returns the immutable session identity.
func (s *Session) ID() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// Manifest returns the persistence manifest that describes this session.
func (s *Session) Manifest() Manifest {
	if s == nil {
		return Manifest{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	manifest := s.manifest
	if manifest.Source != nil {
		source := *manifest.Source
		manifest.Source = &source
	}
	return manifest
}

// EventSequence reports the last accepted sequence. Accepted events may still
// be waiting in the binding's write-behind queue.
func (s *Session) EventSequence() uint64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.next - 1
}

// PrepareBatch validates the logical batch and computes its idempotency digest
// without touching the commit lock. Callers must treat the result as immutable.
func (s *Session) PrepareBatch(operationID string, batch Batch) (PreparedBatch, error) {
	if s == nil {
		return PreparedBatch{}, fmt.Errorf("sessionv3: nil session")
	}
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		operationID = strings.TrimSpace(batch.OperationID)
	}
	if operationID == "" || len(batch.Events) == 0 {
		return PreparedBatch{}, fmt.Errorf("sessionv3: operation id and events are required")
	}
	turnID := strings.TrimSpace(batch.TurnID)
	if turnID == "" && batchContains(batch.Events, "turn/start") {
		turnID = deterministicID("turn\x00" + s.id + "\x00" + operationID)
	}
	events := cloneEvents(batch.Events)
	for i := range events {
		event := &events[i]
		event.Kind = strings.TrimSpace(event.Kind)
		if event.Kind == "" {
			return PreparedBatch{}, fmt.Errorf("sessionv3: events[%d].kind is required", i)
		}
		if !event.Optional && !ProjectionKinds[event.Kind] {
			return PreparedBatch{}, fmt.Errorf("%w: unknown required event %q", ErrUnsupportedVersion, event.Kind)
		}
		// The durable sequence is assigned at commit time so a rejected batch
		// never consumes one.
		event.Sequence = 0
	}
	// The digest covers exactly what the caller supplied. Generated event ids
	// are deliberately excluded so retrying the same logical batch is
	// idempotent instead of reporting a spurious operation conflict.
	hash, err := hashOperation(s.id, turnID, events)
	if err != nil {
		return PreparedBatch{}, err
	}
	for i := range events {
		if events[i].ID == "" {
			events[i].ID = randomID()
		}
	}
	return PreparedBatch{operationID: operationID, turnID: turnID, events: events, hash: hash}, nil
}

// CommitPrepared appends an already validated batch under one short memory
// lock. The persistence binding only receives an immutable batch into its
// write-behind queue, so this never performs file I/O and never blocks on a
// subscriber.
func (s *Session) CommitPrepared(prepared PreparedBatch) (Commit, error) {
	if s == nil {
		return Commit{}, fmt.Errorf("sessionv3: nil session")
	}
	if prepared.Empty() {
		return Commit{}, fmt.Errorf("sessionv3: operation id and events are required")
	}
	s.mu.Lock()
	if s.readOnly {
		s.mu.Unlock()
		return Commit{}, ErrReadOnly
	}
	if s.sealed {
		err := s.sealedError
		s.mu.Unlock()
		if err == nil {
			err = osClosedError()
		}
		return Commit{}, err
	}
	if prior, ok := s.operations[prepared.operationID]; ok {
		if prior.hash != prepared.hash {
			s.mu.Unlock()
			return Commit{}, fmt.Errorf("%w: %q", ErrOperationConflict, prepared.operationID)
		}
		commit := cloneCommit(prior.commit)
		s.mu.Unlock()
		return commit, nil
	}
	commit := Commit{
		SchemaVersion: SchemaVersion, Codec: Codec, RecordType: "commit", ID: randomID(),
		OperationID: prepared.operationID, OperationHash: prepared.hash, FirstSequence: s.next,
		EventCount: len(prepared.events), TurnID: prepared.turnID,
		WriterGeneration: s.manifest.WriterGeneration, CreatedAt: time.Now().UTC(),
		Events: cloneEvents(prepared.events),
	}
	for i := range commit.Events {
		commit.Events[i].Sequence = commit.FirstSequence + uint64(i)
	}
	projection := cloneProjection(s.projection)
	if err := applyProjectionCommit(&projection, commit); err != nil {
		s.mu.Unlock()
		return Commit{}, err
	}
	binding := s.binding
	if binding == nil {
		s.mu.Unlock()
		return Commit{}, ErrReadOnly
	}
	// Session and the persistence queue form one in-memory acceptance boundary.
	// accept performs no I/O or callbacks; after projection validation there are
	// no remaining fallible state changes in the closure.
	err := binding.accept(commit, func() {
		s.commits = append(s.commits, commit)
		s.projection = projection
		s.next = commit.LastSequence() + 1
		s.operations[prepared.operationID] = operationRecord{hash: prepared.hash, commit: commit}
	})
	s.mu.Unlock()
	if err != nil {
		return Commit{}, err
	}
	return cloneCommit(commit), nil
}

// AppendBatch is the convenience form used by callers that do not need to
// separate validation from the activity commit gate.
func (s *Session) AppendBatch(ctx context.Context, operationID string, events []Event) (Commit, error) {
	return s.Append(ctx, Batch{OperationID: operationID, Events: events})
}

// Append validates and commits a logical batch in one step.
func (s *Session) Append(ctx context.Context, batch Batch) (Commit, error) {
	if err := ctx.Err(); err != nil {
		return Commit{}, err
	}
	prepared, err := s.PrepareBatch(batch.OperationID, batch)
	if err != nil {
		return Commit{}, err
	}
	return s.CommitPrepared(prepared)
}

// Snapshot returns the full live view, including message and turn history.
func (s *Session) Snapshot() Snapshot { return s.snapshot(true) }

// StateSnapshot omits history so progress notifications do not copy every
// message and completed turn on each activity update.
func (s *Session) StateSnapshot() Snapshot { return s.snapshot(false) }

func (s *Session) snapshot(includeHistory bool) Snapshot {
	if s == nil {
		return Snapshot{PersistenceStatus: PersistenceFailed, PersistenceError: "nil session"}
	}
	s.mu.Lock()
	projection := s.projection
	sequence := s.next - 1
	s.mu.Unlock()
	if !includeHistory {
		projection.Messages, projection.ModelMessages, projection.Turns = nil, nil, nil
	}
	snapshot := Snapshot{EventSequence: sequence, Projection: cloneProjection(projection)}
	if s.binding != nil {
		durable, status, detail := s.binding.progress()
		snapshot.DurableSequence, snapshot.PersistenceStatus, snapshot.PersistenceError = durable, status, detail
	} else {
		snapshot.PersistenceStatus = PersistenceReady
	}
	// Nested provider metadata is immutable internally but Go cannot freeze
	// returned slices. Detach it outside the commit lock before exposing it.
	snapshot.Projection.Messages = detachMessages(snapshot.Projection.Messages)
	snapshot.Projection.ModelMessages = detachMessages(snapshot.Projection.ModelMessages)
	return snapshot
}

// CatalogMetadata returns the rebuildable list projection of this session.
func (s *Session) CatalogMetadata() catalogMetadata {
	if s == nil {
		return catalogMetadata{Version: catalogMetadataVersion, Codec: Codec}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return metadataFromProjection(s.manifest, s.next-1, s.projection)
}

// DeriveMessages returns the model history projection.
func (s *Session) DeriveMessages() []provider.Message {
	return append([]provider.Message(nil), s.Snapshot().Projection.ModelMessages...)
}

// AcceptedPage returns the live accepted prefix, including events that have not
// crossed a durability checkpoint yet. SessionHandle.Read on the physical layer
// continues to expose only durable records.
func (s *Session) AcceptedPage(ctx context.Context, offset uint64, limit int) (EventPage, error) {
	if err := ctx.Err(); err != nil {
		return EventPage{}, err
	}
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 1000 {
		return EventPage{}, fmt.Errorf("sessionv3: read limit must be 1..1000 commits")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	page := EventPage{Commits: []Commit{}}
	for _, commit := range s.commits {
		if commit.LastSequence() <= offset {
			continue
		}
		if len(page.Commits) == limit {
			page.Truncated = true
			break
		}
		page.Commits = append(page.Commits, cloneCommit(commit))
		page.Next = commit.LastSequence()
	}
	return page, nil
}

// Flush drains the write-behind queue and returns the durable sequence.
func (s *Session) Flush(ctx context.Context) (DurableReceipt, error) {
	if s == nil || s.binding == nil {
		return DurableReceipt{}, ErrReadOnly
	}
	return s.binding.Flush(ctx)
}

// Read exposes the durable prefix through a paged read. Events accepted but not
// yet checkpointed are visible through AcceptedPage instead.
func (s *Session) Read(ctx context.Context, offset uint64, limit int) (EventPage, error) {
	handle := s.Handle()
	if handle == nil {
		return EventPage{}, os.ErrClosed
	}
	return handle.Read(ctx, offset, limit)
}

// Close releases persistence ownership for this session. It is idempotent and
// uncancellable in effect: every caller observes the same close result.
func (s *Session) Close(ctx context.Context) error { return s.close(ctx) }

// Sync forces the physical log to stable storage without draining new work.
func (s *Session) Sync(ctx context.Context) (DurableReceipt, error) {
	handle := s.Handle()
	if handle == nil {
		return DurableReceipt{}, ErrReadOnly
	}
	return handle.Sync(ctx)
}

// newReadSession builds a cold session over a read-only handle. It replays
// nothing: cold callers consume the durable prefix through paged Read, which is
// what keeps catalog and history queries independent of log length.
func newReadSession(handle SessionHandle) *Session {
	manifest := handle.Manifest()
	return &Session{
		id: manifest.SessionID, manifest: manifest, next: 1,
		operations: map[string]operationRecord{}, readOnly: true,
		coldHandle: handle,
	}
}

// Handle exposes the physical persistence handle. Callers must not treat it as
// a business-state owner: reads see only the durable prefix.
func (s *Session) Handle() SessionHandle {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.binding == nil {
		return s.coldHandle
	}
	return s.binding.handle
}

// WritableHandle exposes the leased physical handle for fork, export, and
// recovery operations that are defined in terms of durable bytes.
func (s *Session) WritableHandle() WritableSessionHandle {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.binding == nil {
		return nil
	}
	handle, _ := s.binding.handle.(WritableSessionHandle)
	return handle
}

// seal stops accepting new commits. It is the admission boundary that Runtime
// close establishes before the binding is drained.
func (s *Session) seal(err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.sealed = true
	s.sealedError = err
	binding := s.binding
	s.mu.Unlock()
	if binding != nil {
		binding.stopAccepting()
	}
}

// close seals the session, drains the binding, and closes the physical handle.
// It is uncancellable and idempotent: every caller observes the same result.
func (s *Session) close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.seal(osClosedError())
	s.mu.Lock()
	binding, cold := s.binding, s.coldHandle
	s.mu.Unlock()
	if binding != nil {
		return binding.Close(ctx)
	}
	if cold != nil {
		// A cold session holds no binding, so its handle is released directly.
		// Leaving it open would leak the read handle for every catalog scan.
		return cold.Close(ctx)
	}
	return nil
}
