// Package sessionv3 owns Reasonix's linear, append-only session event log.
//
// The package separates three concerns. Session owns the in-memory typed event
// log and its projections. PersistenceBinding owns the write-behind queue and
// durability progress. Store owns the physical JSONL bytes and the writer
// lease. A commit is accepted into Session and the binding's queue before it is
// durable; Flush establishes an explicit semantic checkpoint.
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

	"reasonix/internal/filelock"
	"reasonix/internal/fileutil"
)

const (
	SchemaVersion = 3
	// Codec identifies the final linear session format. Prototype stores used a
	// different codec and must go through the explicit importer.
	Codec             = "reasonix.session.linear/v3.1"
	LegacyLinearCodec = "reasonix.session.linear/v3"
	PrototypeCodec    = "reasonix.session.events/v3"
	LiveBatchDelay    = 200 * time.Millisecond
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

// Store is the physical JSONL handle for one session directory. It owns the
// writer lease, the open file, and the rebuildable sparse offset index. It
// deliberately holds no projection, operation table, or accepted commit list:
// those belong to Session.
type Store struct {
	mu           sync.Mutex
	indexMu      sync.Mutex
	closeOnce    sync.Once
	closeErr     error
	dir          string
	manifest     Manifest
	file         *os.File
	releaseLease func()
	closed       bool
	index        sparseIndex

	writeFn func(context.Context, io.Writer, []byte) error
	syncFn  func(*os.File) error
}

// ID returns the immutable session identity of the physical store.
func (s *Store) ID() string {
	return s.SessionID()
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

// Dir reports the confined directory that backs this handle.
func (s *Store) Dir() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dir
}

// writableFile returns the open append file for durability repair. The caller
// must already hold the writer lease, which the handle acquired at open.
func (s *Store) writableFile() (*os.File, error) {
	if s == nil {
		return nil, os.ErrClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.file == nil {
		return nil, os.ErrClosed
	}
	return s.file, nil
}

// Open opens an existing legacy-compatible path, creating it when absent. It
// returns a live Session: the in-memory log is the caller's business state.
func Open(dir, sessionID string) (*Session, error) {
	return OpenWithOptions(dir, sessionID, OpenOptions{})
}

// OpenWithOptions is the low-level test/import constructor retained while the
// old controller adapter is removed. Production callers use
// FilesystemPersistence, whose Open is strict and never creates a session.
func OpenWithOptions(dir, sessionID string, opts OpenOptions) (*Session, error) {
	if _, err := os.Stat(filepath.Clean(strings.TrimSpace(dir))); os.IsNotExist(err) {
		return CreateWithOptions(dir, sessionID, opts)
	}
	handle, err := openExistingHandle(dir, sessionID, opts)
	if err != nil {
		return nil, err
	}
	return bindSession(handle, opts)
}

func CreateStore(dir, sessionID string) (*Session, error) {
	return CreateWithOptions(dir, sessionID, OpenOptions{})
}

func CreateWithOptions(dir, sessionID string, opts OpenOptions) (*Session, error) {
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
	created = false
	return OpenWithOptions(dir, sessionID, opts)
}

func openExistingHandle(dir, sessionID string, opts OpenOptions) (*Store, error) {
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
	releaseLease, err := acquireSessionWriter(dir)
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
	// Validate the complete prefix before repairing anything. A newer required
	// event or a damaged complete batch must leave the original tail untouched.
	if _, err := Replay(dir, nil); err != nil {
		return fail(err)
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
	writeFn := opts.Write
	if writeFn == nil {
		writeFn = writeAllContext
	}
	syncFn := opts.Sync
	if syncFn == nil {
		syncFn = func(file *os.File) error { return file.Sync() }
	}
	index, err := loadOrBuildSparseIndex(context.Background(), dir, dir)
	if err != nil {
		return closeAndFail(err)
	}
	return &Store{
		dir: dir, manifest: manifest, file: f, releaseLease: releaseLease,
		index: index, writeFn: writeFn, syncFn: syncFn,
	}, nil
}

// bindSession replays the durable prefix and constructs the in-memory Session
// over a binding for the exact handle.
func bindSession(handle *Store, opts OpenOptions) (*Session, error) {
	dir := handle.Dir()
	commits, err := Replay(dir, nil)
	if err != nil {
		_ = handle.Close(context.Background())
		return nil, err
	}
	projection, err := Project(commits)
	if err != nil {
		_ = handle.Close(context.Background())
		return nil, err
	}
	durable := uint64(0)
	if len(commits) > 0 {
		durable = commits[len(commits)-1].LastSequence()
	}
	binding := newPersistenceBinding(handle, dir, durable, opts)
	session := newSession(handle.Manifest().SessionID, handle.Manifest(), commits, projection, binding)
	binding.metadataSource = session.metadataForDurable
	return session, nil
}

// metadataForDurable rebuilds the list projection for the catalog cache once
// the accepted prefix is fully durable.
func (s *Session) metadataForDurable(durable uint64) (catalogMetadata, bool) {
	if s == nil {
		return catalogMetadata{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if durable+1 != s.next {
		return catalogMetadata{}, false
	}
	return metadataFromProjection(s.manifest, durable, s.projection), true
}

func writeManifestFile(path string, manifest Manifest) error {
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFileStrict(path, append(b, '\n'), 0o600)
}

// Append writes already-committed batches in order. Sequence allocation,
// validation, and idempotency belong to Session; this method is only the
// physical hand-off and reports uncertainty rather than guessing.
func (s *Store) Append(ctx context.Context, commits []Commit) error {
	if s == nil {
		return fmt.Errorf("sessionv3: nil store")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(commits) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.file == nil {
		return os.ErrClosed
	}
	s.indexMu.Lock()
	next := s.index.LastSequence + 1
	s.indexMu.Unlock()
	for i, commit := range commits {
		if commit.SchemaVersion != SchemaVersion || commit.Codec != Codec || commit.RecordType != "commit" ||
			commit.ID == "" || commit.OperationID == "" || commit.OperationHash == "" ||
			commit.WriterGeneration != s.manifest.WriterGeneration || commit.FirstSequence != next ||
			commit.EventCount == 0 || commit.EventCount != len(commit.Events) {
			return fmt.Errorf("%w: invalid physical commit %d at sequence %d", ErrDamagedStore, i, next)
		}
		for eventIndex, event := range commit.Events {
			want := commit.FirstSequence + uint64(eventIndex)
			if event.Sequence != want || event.ID == "" || strings.TrimSpace(event.Kind) == "" {
				return fmt.Errorf("%w: invalid physical event at sequence %d", ErrDamagedStore, want)
			}
		}
		next = commit.LastSequence() + 1
	}
	return s.persist(ctx, s.file, commits)
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

// Sync fsyncs the physical log and reports the durable sequence observed on
// disk. It is the handle half of a semantic checkpoint; PersistenceBinding
// pairs it with queue drain.
func (s *Store) Sync(ctx context.Context) (DurableReceipt, error) {
	if s == nil {
		return DurableReceipt{}, os.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return DurableReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.file == nil {
		return DurableReceipt{}, os.ErrClosed
	}
	if err := s.syncFn(s.file); err != nil {
		return DurableReceipt{}, err
	}
	s.indexMu.Lock()
	sequence := s.index.LastSequence
	s.indexMu.Unlock()
	return DurableReceipt{DurableSequence: sequence}, nil
}

func (s *Store) Close(_ context.Context) error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
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
		s.closeErr = closeErr
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
			return fmt.Errorf("%w: decode complete commit: %w", ErrDamagedStore, err)
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

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
