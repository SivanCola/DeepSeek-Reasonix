// Package sessionv3 owns Reasonix's linear, append-only session event log.
//
// Append accepts a logical batch into the live session immediately. Durability
// is deliberately separate: a fixed write-behind window batches accepted
// events, while Flush establishes an explicit semantic checkpoint.
package sessionv3

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/filelock"
	"reasonix/internal/fileutil"
	"reasonix/internal/provider"
)

const (
	SchemaVersion = 3
	// Codec identifies the final linear session format. Prototype stores used a
	// different codec and must go through the explicit importer.
	Codec          = "reasonix.session.linear/v3"
	PrototypeCodec = "reasonix.session.events/v3"
	LiveBatchDelay = 200 * time.Millisecond
)

var (
	ErrUnsupportedVersion   = errors.New("unsupported v3 session version")
	ErrDamagedStore         = errors.New("damaged v3 session store")
	ErrStaleGeneration      = errors.New("stale v3 writer generation")
	ErrOperationConflict    = errors.New("session operation id conflicts with an earlier batch")
	ErrPersistenceUncertain = errors.New("session persistence result is uncertain")
	ErrSessionNotFound      = errors.New("session not found")
	ErrSessionExists        = errors.New("session already exists")
	ErrWriterOwned          = errors.New("session writer is owned by another runtime")
	ErrReadOnly             = errors.New("session handle is read-only")
)

type Manifest struct {
	SchemaVersion    int       `json:"schemaVersion"`
	Codec            string    `json:"codec"`
	SessionID        string    `json:"sessionId"`
	CreatedAt        time.Time `json:"createdAt"`
	WriterGeneration uint64    `json:"writerGeneration"`
	InheritedEvents  uint64    `json:"inheritedEventCount,omitempty"`
	Source           *Source   `json:"source,omitempty"`
}

type Source struct {
	Path         string `json:"path"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	Version      string `json:"version,omitempty"`
	LegacyHeadID string `json:"legacyHeadId,omitempty"`
}

type Event struct {
	ID       string          `json:"id"`
	Sequence uint64          `json:"seq"`
	Kind     string          `json:"kind"`
	Optional bool            `json:"optional,omitempty"`
	Required bool            `json:"required,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

type Commit struct {
	SchemaVersion    int       `json:"schemaVersion"`
	Codec            string    `json:"codec"`
	RecordType       string    `json:"recordType"`
	ID               string    `json:"commitId"`
	OperationID      string    `json:"operationId"`
	OperationHash    string    `json:"operationHash"`
	FirstSequence    uint64    `json:"firstSeq"`
	EventCount       int       `json:"eventCount"`
	TurnID           string    `json:"turnId,omitempty"`
	WriterGeneration uint64    `json:"writerGeneration"`
	CreatedAt        time.Time `json:"createdAt"`
	Events           []Event   `json:"events"`
}

func (c Commit) LastSequence() uint64 {
	if c.EventCount == 0 {
		return c.FirstSequence
	}
	return c.FirstSequence + uint64(c.EventCount) - 1
}

type Batch struct {
	OperationID string
	TurnID      string
	Events      []Event
}

type DurableReceipt struct {
	DurableSequence uint64 `json:"durableSequence"`
}

type PersistenceStatus string

const (
	PersistenceReady     PersistenceStatus = "ready"
	PersistencePending   PersistenceStatus = "pending"
	PersistenceFailed    PersistenceStatus = "failed"
	PersistenceUncertain PersistenceStatus = "uncertain"
)

type Snapshot struct {
	EventSequence     uint64
	DurableSequence   uint64
	PersistenceStatus PersistenceStatus
	PersistenceError  string
	Projection        Projection
}

type timerHandle interface{ Stop() bool }

type OpenOptions struct {
	AfterFunc func(time.Duration, func()) timerHandle
	Write     func(context.Context, io.Writer, []byte) error
	Sync      func(*os.File) error
}

type operationRecord struct {
	hash   string
	commit Commit
}

type uncertainWrite struct {
	start       int64
	data        []byte
	commitCount int
}

type uncertainAppendError struct {
	cause error
	write uncertainWrite
}

func (e *uncertainAppendError) Error() string {
	return fmt.Sprintf("%v: append at offset %d may have changed the log: %v", ErrPersistenceUncertain, e.write.start, e.cause)
}

func (e *uncertainAppendError) Unwrap() error { return ErrPersistenceUncertain }

type Store struct {
	mu           sync.Mutex
	drainMu      sync.Mutex
	indexMu      sync.Mutex
	closeOnce    sync.Once
	closeErr     error
	dir          string
	manifest     Manifest
	file         *os.File
	releaseLease func()

	next       uint64
	durable    uint64
	commits    []Commit
	pending    []Commit
	projection Projection
	operations map[string]operationRecord

	timer      timerHandle
	draining   bool
	autoPaused bool
	accepting  bool
	closed     bool
	writeErr   error
	uncertain  *uncertainWrite
	index      sparseIndex

	afterFunc func(time.Duration, func()) timerHandle
	writeFn   func(context.Context, io.Writer, []byte) error
	syncFn    func(*os.File) error
}

func (s *Store) SessionID() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manifest.SessionID
}

func (s *Store) Manifest() Manifest {
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

func Open(dir, sessionID string) (*Store, error) {
	return OpenWithOptions(dir, sessionID, OpenOptions{})
}

// OpenWithOptions is the low-level test/import constructor retained while the
// old controller adapter is removed. Production callers use
// FilesystemPersistence, whose Open is strict and never creates a session.
func OpenWithOptions(dir, sessionID string, opts OpenOptions) (*Store, error) {
	if _, err := os.Stat(filepath.Clean(strings.TrimSpace(dir))); os.IsNotExist(err) {
		return CreateWithOptions(dir, sessionID, opts)
	}
	return openExistingWithOptions(dir, sessionID, opts)
}

func CreateStore(dir, sessionID string) (*Store, error) {
	return CreateWithOptions(dir, sessionID, OpenOptions{})
}

func CreateWithOptions(dir, sessionID string, opts OpenOptions) (*Store, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	sessionID = strings.TrimSpace(sessionID)
	if dir == "." || sessionID == "" {
		return nil, fmt.Errorf("sessionv3: directory and session id are required")
	}
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return nil, err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrSessionExists, sessionID)
		}
		return nil, err
	}
	created := true
	defer func() {
		if created {
			_ = os.RemoveAll(dir)
		}
	}()
	manifest := Manifest{SchemaVersion: SchemaVersion, Codec: Codec, SessionID: sessionID, CreatedAt: time.Now().UTC()}
	if err := writeManifestFile(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		return nil, err
	}
	if err := fileutil.AtomicWriteFileStrict(filepath.Join(dir, "events.jsonl"), nil, 0o600); err != nil {
		return nil, err
	}
	store, err := openExistingWithOptions(dir, sessionID, opts)
	if err != nil {
		return nil, err
	}
	created = false
	return store, nil
}

func openExistingWithOptions(dir, sessionID string, opts OpenOptions) (*Store, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	sessionID = strings.TrimSpace(sessionID)
	if dir == "." || sessionID == "" {
		return nil, fmt.Errorf("sessionv3: directory and session id are required")
	}
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("sessionv3: session path is not a directory: %s", dir)
	}
	eventsPath := filepath.Join(dir, "events.jsonl")
	releaseLease, err := filelock.TryAcquire(filepath.Join(dir, "writer.lock"))
	if err != nil {
		if errors.Is(err, filelock.ErrHeld) {
			return nil, fmt.Errorf("%w: %s", ErrWriterOwned, sessionID)
		}
		return nil, err
	}
	fail := func(err error) (*Store, error) {
		releaseLease()
		return nil, err
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	manifest, err := readManifest(manifestPath)
	if err != nil {
		return fail(err)
	}
	if manifest.SessionID != sessionID {
		return fail(fmt.Errorf("sessionv3: manifest belongs to %q", manifest.SessionID))
	}
	if torn, err := hasTornTail(eventsPath); err != nil {
		return fail(err)
	} else if torn {
		// Cold readers deliberately stop at the last complete record. A writer
		// may repair that tail only after acquiring the exclusive lease above:
		// preserve the original bytes first, then truncate back to the durable
		// commit boundary. This never invents or partially replays an event.
		if _, err := preserveAndTruncateTornTail(eventsPath); err != nil {
			return fail(fmt.Errorf("recover torn v3 tail: %w", err))
		}
	}
	commits, err := Replay(dir, nil)
	if err != nil {
		return fail(err)
	}
	projection, err := Project(commits)
	if err != nil {
		return fail(err)
	}
	manifest.WriterGeneration++
	if err := writeManifestFile(manifestPath, manifest); err != nil {
		return fail(err)
	}
	f, err := os.OpenFile(eventsPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return fail(err)
	}
	closeAndFail := func(err error) (*Store, error) {
		_ = f.Close()
		return fail(err)
	}
	after := opts.AfterFunc
	if after == nil {
		after = func(delay time.Duration, fire func()) timerHandle { return time.AfterFunc(delay, fire) }
	}
	writeFn := opts.Write
	if writeFn == nil {
		writeFn = writeAllContext
	}
	syncFn := opts.Sync
	if syncFn == nil {
		syncFn = func(file *os.File) error { return file.Sync() }
	}
	operations := make(map[string]operationRecord, len(commits))
	for _, commit := range commits {
		if prior, ok := operations[commit.OperationID]; ok && prior.hash != commit.OperationHash {
			return closeAndFail(fmt.Errorf("%w: conflicting persisted operation %q", ErrDamagedStore, commit.OperationID))
		}
		operations[commit.OperationID] = operationRecord{hash: commit.OperationHash, commit: commit}
	}
	next := uint64(1)
	durable := uint64(0)
	if len(commits) > 0 {
		durable = commits[len(commits)-1].LastSequence()
		next = durable + 1
	}
	index, err := loadOrBuildSparseIndex(context.Background(), dir, dir)
	if err != nil {
		return closeAndFail(err)
	}
	return &Store{
		dir: dir, manifest: manifest, file: f, releaseLease: releaseLease,
		next: next, durable: durable, commits: commits, projection: projection,
		operations: operations, accepting: true, index: index,
		afterFunc: after, writeFn: writeFn, syncFn: syncFn,
	}, nil
}

func writeManifestFile(path string, manifest Manifest) error {
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFileStrict(path, append(b, '\n'), 0o600)
}

func (s *Store) Append(ctx context.Context, batch Batch) (Commit, error) {
	if s == nil {
		return Commit{}, fmt.Errorf("sessionv3: nil store")
	}
	if err := ctx.Err(); err != nil {
		return Commit{}, err
	}
	batch.OperationID = strings.TrimSpace(batch.OperationID)
	if batch.OperationID == "" || len(batch.Events) == 0 {
		return Commit{}, fmt.Errorf("sessionv3: operation id and events are required")
	}
	turnID := strings.TrimSpace(batch.TurnID)
	if turnID == "" && batchContains(batch.Events, "turn/start") {
		turnID = deterministicID("turn\x00" + s.manifest.SessionID + "\x00" + batch.OperationID)
	}
	hash, err := hashOperation(s.manifest.SessionID, turnID, batch.Events)
	if err != nil {
		return Commit{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.accepting || s.closed || s.file == nil {
		return Commit{}, os.ErrClosed
	}
	if prior, ok := s.operations[batch.OperationID]; ok {
		if prior.hash != hash {
			return Commit{}, fmt.Errorf("%w: %q", ErrOperationConflict, batch.OperationID)
		}
		return cloneCommit(prior.commit), nil
	}
	commit := Commit{
		SchemaVersion: SchemaVersion, Codec: Codec, RecordType: "commit", ID: randomID(),
		OperationID: batch.OperationID, OperationHash: hash, FirstSequence: s.next,
		EventCount: len(batch.Events), TurnID: turnID, WriterGeneration: s.manifest.WriterGeneration,
		CreatedAt: time.Now().UTC(), Events: cloneEvents(batch.Events),
	}
	for i := range commit.Events {
		e := &commit.Events[i]
		e.Kind = strings.TrimSpace(e.Kind)
		if e.Kind == "" {
			return Commit{}, fmt.Errorf("sessionv3: events[%d].kind is required", i)
		}
		if !e.Optional && !ProjectionKinds[e.Kind] {
			return Commit{}, fmt.Errorf("%w: unknown required event %q", ErrUnsupportedVersion, e.Kind)
		}
		if e.ID == "" {
			e.ID = randomID()
		}
		e.Sequence = commit.FirstSequence + uint64(i)
	}
	projection := cloneProjection(s.projection)
	if err := applyProjectionCommit(&projection, commit); err != nil {
		return Commit{}, err
	}
	s.commits = append(s.commits, commit)
	s.pending = append(s.pending, commit)
	s.projection = projection
	s.next = commit.LastSequence() + 1
	s.operations[batch.OperationID] = operationRecord{hash: hash, commit: commit}
	if !s.autoPaused && !s.draining && s.timer == nil {
		s.scheduleDrainLocked()
	}
	return cloneCommit(commit), nil
}

func (s *Store) scheduleDrainLocked() {
	s.timer = s.afterFunc(LiveBatchDelay, func() { _ = s.drain(context.Background(), false) })
}

func (s *Store) Flush(ctx context.Context) (DurableReceipt, error) {
	if s == nil {
		return DurableReceipt{}, fmt.Errorf("sessionv3: nil store")
	}
	if err := ctx.Err(); err != nil {
		return DurableReceipt{}, err
	}
	// Once a physical append has begun, one caller abandoning its wait cannot
	// safely cancel that write or make another waiter guess whether bytes reached
	// disk. drainMu merges concurrent callers onto the same ordered write chain;
	// each caller may still stop waiting through its own context.
	done := make(chan error, 1)
	go func() { done <- s.drain(context.Background(), true) }()
	select {
	case err := <-done:
		s.mu.Lock()
		receipt := DurableReceipt{DurableSequence: s.durable}
		s.mu.Unlock()
		return receipt, err
	case <-ctx.Done():
		s.mu.Lock()
		receipt := DurableReceipt{DurableSequence: s.durable}
		s.mu.Unlock()
		return receipt, ctx.Err()
	}
}

func (s *Store) drain(ctx context.Context, explicit bool) error {
	s.drainMu.Lock()
	defer s.drainMu.Unlock()
	for {
		s.mu.Lock()
		if s.closed || s.file == nil {
			s.mu.Unlock()
			return os.ErrClosed
		}
		if s.timer != nil {
			s.timer.Stop()
			s.timer = nil
		}
		if len(s.pending) == 0 {
			s.draining = false
			s.mu.Unlock()
			return nil
		}
		uncertain := cloneUncertainWrite(s.uncertain)
		if uncertain != nil && !explicit {
			err := s.writeErr
			s.draining = false
			s.mu.Unlock()
			return err
		}
		s.draining = true
		pending := cloneCommits(s.pending)
		file := s.file
		s.mu.Unlock()

		alreadyPersisted := false
		var err error
		if uncertain != nil {
			alreadyPersisted, err = s.reconcileUncertain(ctx, file, *uncertain)
		}
		if err == nil && alreadyPersisted {
			if indexErr := s.rebuildWriterIndex(file); indexErr != nil {
				err = indexErr
			}
			confirmed := uncertain.commitCount
			if confirmed <= 0 || confirmed > len(pending) {
				err = fmt.Errorf("%w: uncertain batch commit count %d exceeds pending prefix %d", ErrDamagedStore, confirmed, len(pending))
			} else {
				s.mu.Lock()
				if len(s.pending) < confirmed || !sameCommitPrefix(s.pending, pending[:confirmed]) {
					err = fmt.Errorf("%w: uncertain batch no longer matches pending prefix", ErrDamagedStore)
					s.writeErr = err
					s.autoPaused = true
					s.draining = false
					s.mu.Unlock()
					return err
				}
				s.pending = s.pending[confirmed:]
				s.durable = pending[confirmed-1].LastSequence()
				s.writeErr = nil
				s.uncertain = nil
				s.autoPaused = false
				if len(s.pending) == 0 {
					s.draining = false
					s.mu.Unlock()
					return nil
				}
				s.mu.Unlock()
				continue
			}
		}
		if err == nil && !alreadyPersisted {
			err = s.persist(ctx, file, pending)
		}
		s.mu.Lock()
		if err != nil {
			s.draining = false
			s.writeErr = err
			var uncertainErr *uncertainAppendError
			if errors.As(err, &uncertainErr) {
				copy := uncertainErr.write
				copy.data = append([]byte(nil), copy.data...)
				s.uncertain = &copy
			}
			s.autoPaused = true
			s.mu.Unlock()
			return err
		}
		if len(s.pending) < len(pending) || !sameCommitPrefix(s.pending, pending) {
			s.draining = false
			s.writeErr = fmt.Errorf("%w: pending batch order changed", ErrDamagedStore)
			s.autoPaused = true
			err := s.writeErr
			s.mu.Unlock()
			return err
		}
		s.pending = s.pending[len(pending):]
		s.durable = pending[len(pending)-1].LastSequence()
		s.writeErr = nil
		s.uncertain = nil
		s.autoPaused = false
		if len(s.pending) == 0 {
			s.draining = false
			s.mu.Unlock()
			return nil
		}
		// Match DSH's drain chain: once a batch starts writing, events accepted
		// during that write are drained immediately in the next physical batch.
		// The fixed 200ms window applies only to the first pending batch.
		s.mu.Unlock()
	}
}

func (s *Store) persist(ctx context.Context, file *os.File, commits []Commit) error {
	written, lengths, err := encodeCommitLines(commits)
	if err != nil {
		return err
	}
	start, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if err := s.writeFn(ctx, file, written); err != nil {
		end, statErr := file.Seek(0, io.SeekEnd)
		if statErr == nil && end == start {
			return err
		}
		if statErr == nil && end == start+int64(len(written)) {
			if syncErr := s.syncFn(file); syncErr == nil {
				s.recordPersistedIndex(file, start, commits, lengths)
				return nil
			}
		}
		return &uncertainAppendError{cause: err, write: uncertainWrite{start: start, data: append([]byte(nil), written...), commitCount: len(commits)}}
	}
	if err := s.syncFn(file); err != nil {
		return &uncertainAppendError{cause: fmt.Errorf("fsync: %w", err), write: uncertainWrite{start: start, data: append([]byte(nil), written...), commitCount: len(commits)}}
	}
	s.recordPersistedIndex(file, start, commits, lengths)
	return nil
}

func encodeCommits(commits []Commit) ([]byte, error) {
	written, _, err := encodeCommitLines(commits)
	return written, err
}

func encodeCommitLines(commits []Commit) ([]byte, []int, error) {
	var data bytes.Buffer
	lengths := make([]int, 0, len(commits))
	for _, commit := range commits {
		line, err := json.Marshal(commit)
		if err != nil {
			return nil, nil, err
		}
		data.Write(line)
		data.WriteByte('\n')
		lengths = append(lengths, len(line)+1)
	}
	return data.Bytes(), lengths, nil
}

// reconcileUncertain proves whether the prior append completed before retrying.
// The writer lease and drainMu make this a single-owner repair operation. A
// partial tail is preserved byte-for-byte before truncation; a mismatching or
// unexpectedly extended tail remains recovery-required rather than guessed.
func (s *Store) reconcileUncertain(ctx context.Context, file *os.File, uncertain uncertainWrite) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() < uncertain.start {
		return false, fmt.Errorf("%w: log shrank below uncertain offset %d", ErrPersistenceUncertain, uncertain.start)
	}
	tailLen := info.Size() - uncertain.start
	if tailLen == 0 {
		return false, nil
	}
	tail := make([]byte, tailLen)
	if _, err := file.ReadAt(tail, uncertain.start); err != nil {
		return false, fmt.Errorf("%w: inspect uncertain tail: %v", ErrPersistenceUncertain, err)
	}
	if int64(len(uncertain.data)) == tailLen && bytes.Equal(tail, uncertain.data) {
		if err := s.syncFn(file); err != nil {
			return false, &uncertainAppendError{cause: fmt.Errorf("fsync verified append: %w", err), write: uncertain}
		}
		return true, nil
	}
	if tailLen < int64(len(uncertain.data)) && bytes.Equal(tail, uncertain.data[:len(tail)]) {
		backup := filepath.Join(s.dir, fmt.Sprintf("events.uncertain-%d.tail", time.Now().UTC().UnixNano()))
		if err := os.WriteFile(backup, tail, 0o600); err != nil {
			return false, fmt.Errorf("%w: preserve partial tail: %v", ErrPersistenceUncertain, err)
		}
		if err := file.Truncate(uncertain.start); err != nil {
			return false, fmt.Errorf("%w: truncate preserved partial tail: %v", ErrPersistenceUncertain, err)
		}
		if _, err := file.Seek(0, io.SeekEnd); err != nil {
			return false, fmt.Errorf("%w: seek repaired log: %v", ErrPersistenceUncertain, err)
		}
		if err := s.syncFn(file); err != nil {
			return false, fmt.Errorf("%w: sync repaired log: %v", ErrPersistenceUncertain, err)
		}
		return false, nil
	}
	return false, fmt.Errorf("%w: on-disk tail does not match batch at offset %d", ErrPersistenceUncertain, uncertain.start)
}

func cloneUncertainWrite(in *uncertainWrite) *uncertainWrite {
	if in == nil {
		return nil
	}
	out := *in
	out.data = append([]byte(nil), in.data...)
	return &out
}

func (s *Store) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{PersistenceStatus: PersistenceFailed, PersistenceError: "nil session store"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	status := PersistenceReady
	if s.writeErr != nil {
		status = PersistenceFailed
		if errors.Is(s.writeErr, ErrPersistenceUncertain) {
			status = PersistenceUncertain
		}
	} else if len(s.pending) > 0 || s.draining {
		status = PersistencePending
	}
	return Snapshot{
		EventSequence: s.next - 1, DurableSequence: s.durable,
		PersistenceStatus: status, PersistenceError: errorString(s.writeErr),
		Projection: cloneProjection(s.projection),
	}
}

func (s *Store) Close(_ context.Context) error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.accepting = false
		s.mu.Unlock()
		// Teardown is deliberately uncancellable: once closing begins, every
		// caller observes the same completed drain/release result.
		_, flushErr := s.Flush(context.Background())
		s.mu.Lock()
		if s.timer != nil {
			s.timer.Stop()
			s.timer = nil
		}
		file := s.file
		s.file = nil
		s.closed = true
		releaseLease := s.releaseLease
		s.releaseLease = nil
		s.mu.Unlock()
		var closeErr error
		if file != nil {
			closeErr = file.Close()
		}
		if releaseLease != nil {
			releaseLease()
		}
		s.closeErr = errors.Join(flushErr, closeErr)
	})
	return s.closeErr
}

func writeAllContext(ctx context.Context, w io.Writer, data []byte) error {
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

// Replay returns the complete durable prefix. It ignores only an unterminated
// final record; a later write owner preserves and repairs that tail after it
// acquires the exclusive lease.
func Replay(dir string, knownKinds map[string]bool) ([]Commit, error) {
	commits := []Commit{}
	err := scanDurableCommits(dir, knownKinds, func(commit Commit) bool {
		commits = append(commits, commit)
		return true
	})
	return commits, err
}

// scanDurableCommits validates records in sequence and lets paged readers stop
// without materializing the rest of a large log. The next page resumes from a
// sequence cursor; a rebuildable offset index can optimize seeking without
// changing this validation contract.
func scanDurableCommits(dir string, knownKinds map[string]bool, visit func(Commit) bool) error {
	if knownKinds == nil {
		knownKinds = ProjectionKinds
	}
	file, err := os.Open(filepath.Join(dir, "events.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	return scanCommitFile(file, 0, 1, knownKinds, func(_ int64, commit Commit) bool {
		if visit == nil {
			return true
		}
		return visit(commit)
	})
}

// scanCommitFile validates complete records beginning at an already validated
// commit boundary. startOffset and nextSequence come from the rebuildable
// sparse index; callers that do not have one pass 0 and 1.
func scanCommitFile(file *os.File, startOffset int64, nextSequence uint64, knownKinds map[string]bool, visit func(int64, Commit) bool) error {
	return scanCommitFileCodec(file, startOffset, nextSequence, Codec, knownKinds, visit)
}

func scanCommitFileCodec(file *os.File, startOffset int64, nextSequence uint64, codec string, knownKinds map[string]bool, visit func(int64, Commit) bool) error {
	if knownKinds == nil {
		knownKinds = ProjectionKinds
	}
	if _, err := file.Seek(startOffset, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(file, 64<<10)
	next := nextSequence
	offset := startOffset
	operations := map[string]string{}
	for {
		recordOffset := offset
		line, readErr := reader.ReadBytes('\n')
		if errors.Is(readErr, io.EOF) {
			// Cold readers expose only the complete durable prefix. The exclusive
			// writer path preserves and repairs this tail before accepting work.
			break
		}
		if readErr != nil {
			return readErr
		}
		offset += int64(len(line))
		if len(line) > 64<<20 {
			return fmt.Errorf("%w: event record exceeds 64 MiB", ErrDamagedStore)
		}
		var commit Commit
		if err := json.Unmarshal(bytes.TrimSuffix(line, []byte{'\n'}), &commit); err != nil {
			return fmt.Errorf("%w: decode complete commit: %v", ErrDamagedStore, err)
		}
		if commit.SchemaVersion != SchemaVersion || commit.Codec != codec {
			return fmt.Errorf("%w: event codec", ErrUnsupportedVersion)
		}
		if commit.RecordType != "commit" || commit.ID == "" || commit.OperationID == "" ||
			commit.OperationHash == "" || commit.WriterGeneration == 0 || commit.FirstSequence != next ||
			commit.EventCount != len(commit.Events) || commit.EventCount == 0 {
			return fmt.Errorf("%w: invalid commit boundary at sequence %d", ErrDamagedStore, next)
		}
		if prior, ok := operations[commit.OperationID]; ok && prior != commit.OperationHash {
			return fmt.Errorf("%w: conflicting operation %q", ErrDamagedStore, commit.OperationID)
		}
		operations[commit.OperationID] = commit.OperationHash
		for i, event := range commit.Events {
			if event.Sequence != next+uint64(i) || event.ID == "" || strings.TrimSpace(event.Kind) == "" {
				return fmt.Errorf("%w: invalid event at sequence %d", ErrDamagedStore, next+uint64(i))
			}
			if !event.Optional && !knownKinds[event.Kind] {
				return fmt.Errorf("%w: unknown required event %q", ErrUnsupportedVersion, event.Kind)
			}
		}
		next = commit.LastSequence() + 1
		if visit != nil && !visit(recordOffset, commit) {
			return nil
		}
	}
	return nil
}

func hasTornTail(path string) (bool, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return false, err
	}
	if _, err := file.Seek(-1, io.SeekEnd); err != nil {
		return false, err
	}
	var last [1]byte
	if _, err := io.ReadFull(file, last[:]); err != nil {
		return false, err
	}
	return last[0] != '\n', nil
}

func preserveAndTruncateTornTail(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) == 0 || data[len(data)-1] == '\n' {
		return "", nil
	}
	cut := bytes.LastIndexByte(data, '\n') + 1
	tail := append([]byte(nil), data[cut:]...)
	backup := filepath.Join(filepath.Dir(path), fmt.Sprintf("events.torn-%d.tail", time.Now().UTC().UnixNano()))
	if err := fileutil.AtomicWriteFileStrict(backup, tail, 0o600); err != nil {
		return "", fmt.Errorf("preserve original tail: %w", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return "", err
	}
	if err := file.Truncate(int64(cut)); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("truncate to durable prefix: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("sync durable prefix: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return backup, nil
}

func hashOperation(sessionID, turnID string, events []Event) (string, error) {
	type inputEvent struct {
		ID       string          `json:"id,omitempty"`
		Kind     string          `json:"kind"`
		Optional bool            `json:"optional,omitempty"`
		Payload  json.RawMessage `json:"payload,omitempty"`
	}
	inputs := make([]inputEvent, len(events))
	for i, event := range events {
		inputs[i] = inputEvent{ID: event.ID, Kind: strings.TrimSpace(event.Kind), Optional: event.Optional, Payload: event.Payload}
	}
	b, err := json.Marshal(struct {
		SessionID string       `json:"sessionId"`
		TurnID    string       `json:"turnId"`
		Events    []inputEvent `json:"events"`
	}{SessionID: sessionID, TurnID: turnID, Events: inputs})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func deterministicID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:16])
}

func batchContains(events []Event, kind string) bool {
	for _, event := range events {
		if strings.TrimSpace(event.Kind) == kind {
			return true
		}
	}
	return false
}

func sameCommitPrefix(all, prefix []Commit) bool {
	for i := range prefix {
		if all[i].ID != prefix[i].ID {
			return false
		}
	}
	return true
}

func cloneEvents(events []Event) []Event {
	out := make([]Event, len(events))
	copy(out, events)
	for i := range out {
		out[i].Payload = append(json.RawMessage(nil), out[i].Payload...)
	}
	return out
}

func cloneCommit(commit Commit) Commit {
	commit.Events = cloneEvents(commit.Events)
	return commit
}

func cloneCommits(commits []Commit) []Commit {
	out := make([]Commit, len(commits))
	for i := range commits {
		out[i] = cloneCommit(commits[i])
	}
	return out
}

func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

type Projection struct {
	CommittedSequence uint64
	TurnID            string
	TurnStatus        event.TurnStatus
	CurrentTurnStart  uint64
	Turns             []TurnBoundary
	Messages          []provider.Message
	// ModelMessages is the exact provider-visible projection. Canonical Messages
	// remains the complete UI/history transcript; compaction replaces only this
	// view and never deletes the underlying business history.
	ModelMessages []provider.Message
	Todos         []event.Todo
	TodoWritten   bool
	Interactions  map[string]string
	ActiveTools   map[string]string
	Recovery      *event.RecoveryStatus
	PlanState     json.RawMessage
	GoalState     json.RawMessage
	Title         string
	ModelRef      string
	ModelIdentity string
}

type TurnBoundary struct {
	TurnID        string           `json:"turnId"`
	StartSequence uint64           `json:"startSequence"`
	EndSequence   uint64           `json:"endSequence"`
	Status        event.TurnStatus `json:"status"`
}

var ProjectionKinds = map[string]bool{
	"message/complete": true, "message/upsert": true, "assistant/attempt": true,
	"tool/call": true, "tool/start": true, "tool/result": true,
	"turn/start": true, "turn/end": true, "step/start": true, "step/end": true,
	"todo/write": true, "interaction/created": true, "interaction/resolved": true,
	"plan/state": true, "goal/state": true, "session/title": true, "session/config": true,
	"model/context-replace": true, "history/replace": true,
	"compaction": true, "runtime/recovery": true, "legacy/import": true,
	"diagnostic": true,
}

var PrototypeProjectionKinds = func() map[string]bool {
	kinds := make(map[string]bool, len(ProjectionKinds)+1)
	for kind, supported := range ProjectionKinds {
		kinds[kind] = supported
	}
	kinds["context/replace"] = true
	return kinds
}()

func Project(commits []Commit) (Projection, error) {
	projection := Projection{Todos: []event.Todo{}, Interactions: map[string]string{}, ActiveTools: map[string]string{}}
	for _, commit := range commits {
		if err := applyProjectionCommit(&projection, commit); err != nil {
			return Projection{}, err
		}
	}
	return projection, nil
}

func applyProjectionCommit(projection *Projection, commit Commit) error {
	if projection.Interactions == nil {
		projection.Interactions = map[string]string{}
	}
	if projection.ActiveTools == nil {
		projection.ActiveTools = map[string]string{}
	}
	for _, ev := range commit.Events {
		projection.CommittedSequence = ev.Sequence
		switch ev.Kind {
		case "legacy/import":
			var body struct {
				Source        Source             `json:"source"`
				Messages      []provider.Message `json:"messages"`
				Goal          json.RawMessage    `json:"goal,omitempty"`
				ModelRef      string             `json:"modelRef,omitempty"`
				ModelIdentity string             `json:"modelIdentity,omitempty"`
			}
			if err := strictPayload(ev.Payload, &body); err != nil || body.Messages == nil {
				return damagedPayload(ev, err)
			}
			projection.Messages = append([]provider.Message{}, body.Messages...)
			projection.ModelMessages = append([]provider.Message{}, provider.ModelMessages(body.Messages)...)
			projection.GoalState = cloneRaw(body.Goal)
			projection.ModelRef = strings.TrimSpace(body.ModelRef)
			projection.ModelIdentity = strings.TrimSpace(body.ModelIdentity)
		case "message/complete":
			var body struct {
				Message *provider.Message `json:"message"`
			}
			if err := strictPayload(ev.Payload, &body); err != nil || body.Message == nil || body.Message.ID == "" {
				return damagedPayload(ev, err)
			}
			if projectionMessageIndex(projection.Messages, body.Message.ID) >= 0 {
				return damagedPayload(ev, fmt.Errorf("duplicate stable message id %q", body.Message.ID))
			}
			projection.Messages = append(projection.Messages, *body.Message)
			projection.ModelMessages = append(projection.ModelMessages, provider.ModelMessages([]provider.Message{*body.Message})...)
		case "message/upsert":
			var body struct {
				Message *provider.Message `json:"message"`
			}
			if err := strictPayload(ev.Payload, &body); err != nil || body.Message == nil || body.Message.ID == "" {
				return damagedPayload(ev, err)
			}
			if !replaceProjectionMessage(projection.Messages, *body.Message) {
				projection.Messages = append(projection.Messages, *body.Message)
			}
			visible := provider.ModelMessages([]provider.Message{*body.Message})
			modelIndex := projectionMessageIndex(projection.ModelMessages, body.Message.ID)
			if modelIndex >= 0 {
				if len(visible) == 0 {
					projection.ModelMessages = append(projection.ModelMessages[:modelIndex], projection.ModelMessages[modelIndex+1:]...)
				} else {
					projection.ModelMessages[modelIndex] = visible[0]
				}
			} else if len(visible) > 0 {
				// Upserts are normally metadata changes to an existing message. The
				// append case is retained for explicitly-created records.
				projection.ModelMessages = append(projection.ModelMessages, visible[0])
			}
		case "assistant/attempt":
			var body struct {
				ID        string `json:"id"`
				MessageID string `json:"messageId,omitempty"`
				Action    string `json:"action"`
				Attempt   int    `json:"attempt,omitempty"`
				Max       int    `json:"max,omitempty"`
				Reason    string `json:"reason,omitempty"`
			}
			if err := strictPayload(ev.Payload, &body); err != nil || body.ID == "" || (body.Action != "begin" && body.Action != "discard" && body.Action != "commit") {
				return damagedPayload(ev, err)
			}
		case "history/replace":
			var body struct {
				Messages []provider.Message `json:"messages"`
				Reason   string             `json:"reason,omitempty"`
				Sources  []uint64           `json:"sourceSequences,omitempty"`
			}
			if err := strictPayload(ev.Payload, &body); err != nil || body.Messages == nil {
				return damagedPayload(ev, err)
			}
			projection.Messages = append([]provider.Message(nil), body.Messages...)
			projection.ModelMessages = append([]provider.Message(nil), provider.ModelMessages(body.Messages)...)
		case "model/context-replace":
			var body struct {
				Messages []provider.Message `json:"messages"`
				Reason   string             `json:"reason,omitempty"`
				Sources  []uint64           `json:"sourceSequences,omitempty"`
			}
			if err := strictPayload(ev.Payload, &body); err != nil || body.Messages == nil {
				return damagedPayload(ev, err)
			}
			projection.ModelMessages = append([]provider.Message(nil), body.Messages...)
		case "session/title":
			var body struct {
				Title string `json:"title"`
			}
			if err := strictPayload(ev.Payload, &body); err != nil {
				return damagedPayload(ev, err)
			}
			projection.Title = body.Title
		case "session/config":
			var body struct {
				ModelRef      string `json:"modelRef"`
				ModelIdentity string `json:"modelIdentity,omitempty"`
			}
			if err := strictPayload(ev.Payload, &body); err != nil || strings.TrimSpace(body.ModelRef) == "" {
				return damagedPayload(ev, err)
			}
			projection.ModelRef = strings.TrimSpace(body.ModelRef)
			projection.ModelIdentity = strings.TrimSpace(body.ModelIdentity)
		case "compaction":
			var body struct {
				Messages []provider.Message `json:"messages"`
				Trigger  string             `json:"trigger,omitempty"`
				Sources  []uint64           `json:"sourceSequences,omitempty"`
			}
			if err := strictPayload(ev.Payload, &body); err != nil || body.Messages == nil {
				return damagedPayload(ev, err)
			}
			projection.ModelMessages = append([]provider.Message(nil), body.Messages...)
		case "turn/start":
			projection.TurnID = commit.TurnID
			projection.TurnStatus = event.TurnInProgress
			projection.CurrentTurnStart = ev.Sequence
			projection.Todos, projection.TodoWritten = []event.Todo{}, false
			projection.Recovery = nil
		case "step/start", "step/end":
			var body struct {
				ID     string `json:"id"`
				Status string `json:"status,omitempty"`
			}
			if err := strictPayload(ev.Payload, &body); err != nil || body.ID == "" {
				return damagedPayload(ev, err)
			}
		case "tool/call":
			var body struct {
				ID                string                   `json:"id"`
				Name              string                   `json:"name"`
				Args              string                   `json:"args,omitempty"`
				RunState          provider.ToolRunState    `json:"runState,omitempty"`
				Diagnostic        json.RawMessage          `json:"diagnostic,omitempty"`
				ResolvedName      string                   `json:"resolvedName,omitempty"`
				CapabilityID      string                   `json:"capabilityId,omitempty"`
				ReadOnly          bool                     `json:"readOnly,omitempty"`
				Truncated         bool                     `json:"truncated,omitempty"`
				DurationMs        int64                    `json:"durationMs,omitempty"`
				StartedAt         int64                    `json:"startedAt,omitempty"`
				EndedAt           int64                    `json:"endedAt,omitempty"`
				Partial           bool                     `json:"partial,omitempty"`
				ArgChars          int                      `json:"argChars,omitempty"`
				Refreshed         bool                     `json:"refreshed,omitempty"`
				ParentID          string                   `json:"parentId,omitempty"`
				AttemptID         string                   `json:"attemptId,omitempty"`
				SubagentRef       string                   `json:"subagentRef,omitempty"`
				SubagentStatus    string                   `json:"subagentStatus,omitempty"`
				SubagentErrorCode string                   `json:"subagentErrorCode,omitempty"`
				SubagentRetryable bool                     `json:"subagentRetryable,omitempty"`
				Diff              string                   `json:"diff,omitempty"`
				Added             int                      `json:"added,omitempty"`
				Removed           int                      `json:"removed,omitempty"`
				Profile           json.RawMessage          `json:"profile,omitempty"`
				Execution         json.RawMessage          `json:"execution,omitempty"`
				PresentedFiles    []provider.PresentedFile `json:"presentedFiles,omitempty"`
				WorkspaceMutation bool                     `json:"workspaceMutation,omitempty"`
				WorkspacePaths    []string                 `json:"workspacePaths,omitempty"`
				WorkspaceAllPaths bool                     `json:"workspaceAllPaths,omitempty"`
			}
			if err := strictPayload(ev.Payload, &body); err != nil || body.ID == "" || body.Name == "" {
				return damagedPayload(ev, err)
			}
		case "tool/start":
			var body struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if err := strictPayload(ev.Payload, &body); err != nil || body.ID == "" || body.Name == "" {
				return damagedPayload(ev, err)
			}
			projection.ActiveTools[body.ID] = body.Name
		case "tool/result":
			var body struct {
				ID                string                   `json:"id"`
				Name              string                   `json:"name"`
				Args              string                   `json:"args,omitempty"`
				Error             string                   `json:"error,omitempty"`
				Output            string                   `json:"output,omitempty"`
				State             string                   `json:"state,omitempty"`
				RunState          provider.ToolRunState    `json:"runState,omitempty"`
				Diagnostic        json.RawMessage          `json:"diagnostic,omitempty"`
				ResolvedName      string                   `json:"resolvedName,omitempty"`
				CapabilityID      string                   `json:"capabilityId,omitempty"`
				ReadOnly          bool                     `json:"readOnly,omitempty"`
				Truncated         bool                     `json:"truncated,omitempty"`
				DurationMs        int64                    `json:"durationMs,omitempty"`
				StartedAt         int64                    `json:"startedAt,omitempty"`
				EndedAt           int64                    `json:"endedAt,omitempty"`
				Partial           bool                     `json:"partial,omitempty"`
				ArgChars          int                      `json:"argChars,omitempty"`
				Refreshed         bool                     `json:"refreshed,omitempty"`
				ParentID          string                   `json:"parentId,omitempty"`
				AttemptID         string                   `json:"attemptId,omitempty"`
				SubagentRef       string                   `json:"subagentRef,omitempty"`
				SubagentStatus    string                   `json:"subagentStatus,omitempty"`
				SubagentErrorCode string                   `json:"subagentErrorCode,omitempty"`
				SubagentRetryable bool                     `json:"subagentRetryable,omitempty"`
				Diff              string                   `json:"diff,omitempty"`
				Added             int                      `json:"added,omitempty"`
				Removed           int                      `json:"removed,omitempty"`
				Profile           json.RawMessage          `json:"profile,omitempty"`
				Execution         json.RawMessage          `json:"execution,omitempty"`
				PresentedFiles    []provider.PresentedFile `json:"presentedFiles,omitempty"`
				Todos             []event.Todo             `json:"todos,omitempty"`
				TodoWritten       bool                     `json:"todoWritten,omitempty"`
				WorkspaceMutation bool                     `json:"workspaceMutation,omitempty"`
				WorkspacePaths    []string                 `json:"workspacePaths,omitempty"`
				WorkspaceAllPaths bool                     `json:"workspaceAllPaths,omitempty"`
			}
			if err := strictPayload(ev.Payload, &body); err != nil || body.ID == "" || body.Name == "" {
				return damagedPayload(ev, err)
			}
			delete(projection.ActiveTools, body.ID)
		case "todo/write":
			var body struct {
				Todos []event.Todo `json:"todos"`
			}
			if err := strictPayload(ev.Payload, &body); err != nil || validateTodos(body.Todos) != nil {
				return damagedPayload(ev, err)
			}
			projection.Todos, projection.TodoWritten = append([]event.Todo(nil), body.Todos...), true
		case "interaction/created":
			var body struct {
				ID           string `json:"id"`
				ToolCallID   string `json:"toolCallId,omitempty"`
				Kind         string `json:"kind,omitempty"`
				State        string `json:"state,omitempty"`
				SessionID    string `json:"sessionId,omitempty"`
				HeadID       string `json:"headId,omitempty"`
				TurnID       string `json:"turnId,omitempty"`
				RuntimeEpoch string `json:"runtimeEpoch,omitempty"`
			}
			if err := strictPayload(ev.Payload, &body); err != nil || body.ID == "" || (body.State != "" && body.State != "pending") {
				return damagedPayload(ev, err)
			}
			projection.Interactions[body.ID] = "pending"
		case "interaction/resolved":
			var body struct {
				ID    string `json:"id"`
				State string `json:"state"`
			}
			if err := strictPayload(ev.Payload, &body); err != nil || body.ID == "" || !terminalInteractionState(body.State) {
				return damagedPayload(ev, err)
			}
			delete(projection.Interactions, body.ID)
		case "runtime/recovery":
			var body event.RecoveryStatus
			if err := strictPayload(ev.Payload, &body); err != nil {
				return damagedPayload(ev, err)
			}
			projection.Recovery = &body
		case "plan/state":
			if !validJSONObject(ev.Payload) {
				return damagedPayload(ev, nil)
			}
			projection.PlanState = cloneRaw(ev.Payload)
		case "goal/state":
			if !validJSONObject(ev.Payload) {
				return damagedPayload(ev, nil)
			}
			projection.GoalState = cloneRaw(ev.Payload)
		case "diagnostic":
			if len(ev.Payload) > 0 && !json.Valid(ev.Payload) {
				return damagedPayload(ev, nil)
			}
		case "turn/end":
			var body struct {
				Status event.TurnStatus `json:"status"`
			}
			if err := strictPayload(ev.Payload, &body); err != nil || !body.Status.Terminal() {
				return damagedPayload(ev, err)
			}
			if projection.TurnID != "" && projection.CurrentTurnStart != 0 {
				projection.Turns = append(projection.Turns, TurnBoundary{
					TurnID: projection.TurnID, StartSequence: projection.CurrentTurnStart,
					EndSequence: ev.Sequence, Status: body.Status,
				})
			}
			projection.TurnID = ""
			projection.CurrentTurnStart = 0
			projection.TurnStatus = body.Status
		}
	}
	return nil
}

func replaceProjectionMessage(messages []provider.Message, replacement provider.Message) bool {
	if index := projectionMessageIndex(messages, replacement.ID); index >= 0 {
		messages[index] = replacement
		return true
	}
	return false
}

func projectionMessageIndex(messages []provider.Message, id string) int {
	for i := range messages {
		if messages[i].ID == id {
			return i
		}
	}
	return -1
}

func cloneProjection(projection Projection) Projection {
	projection.Messages = append([]provider.Message(nil), projection.Messages...)
	projection.ModelMessages = append([]provider.Message(nil), projection.ModelMessages...)
	projection.Turns = append([]TurnBoundary(nil), projection.Turns...)
	projection.Todos = append([]event.Todo(nil), projection.Todos...)
	interactions := make(map[string]string, len(projection.Interactions))
	for key, value := range projection.Interactions {
		interactions[key] = value
	}
	projection.Interactions = interactions
	tools := make(map[string]string, len(projection.ActiveTools))
	for key, value := range projection.ActiveTools {
		tools[key] = value
	}
	projection.ActiveTools = tools
	if projection.Recovery != nil {
		recovery := *projection.Recovery
		projection.Recovery = &recovery
	}
	projection.PlanState = cloneRaw(projection.PlanState)
	projection.GoalState = cloneRaw(projection.GoalState)
	return projection
}

func strictPayload(payload json.RawMessage, target any) error {
	if len(payload) == 0 {
		return io.ErrUnexpectedEOF
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func validateTodos(todos []event.Todo) error {
	if todos == nil {
		return fmt.Errorf("todos must be an array")
	}
	seen := make(map[string]bool, len(todos))
	for i, todo := range todos {
		content := strings.TrimSpace(todo.Content)
		if content == "" || content != todo.Content || seen[content] {
			return fmt.Errorf("todos[%d].content is invalid", i)
		}
		seen[content] = true
		switch todo.Status {
		case "pending", "in_progress", "completed":
		default:
			return fmt.Errorf("todos[%d].status is invalid", i)
		}
	}
	return nil
}

func terminalInteractionState(state string) bool {
	switch state {
	case "answered", "rejected", "cancelled", "unavailable":
		return true
	default:
		return false
	}
}

func validJSONObject(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	return len(raw) > 0 && json.Unmarshal(raw, &object) == nil && object != nil
}

func damagedPayload(event Event, cause error) error {
	if cause != nil {
		return fmt.Errorf("%w: invalid %s payload at %d: %v", ErrDamagedStore, event.Kind, event.Sequence, cause)
	}
	return fmt.Errorf("%w: invalid %s payload at %d", ErrDamagedStore, event.Kind, event.Sequence)
}

func cloneRaw(raw json.RawMessage) json.RawMessage { return append(json.RawMessage(nil), raw...) }

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
