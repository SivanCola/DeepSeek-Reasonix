package sessionv3

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"reasonix/internal/provider"
)

// AccessMode separates cold readers from the single leased writer. Read-only
// access never repairs, migrates, truncates, or advances writer generation.
type AccessMode string

const (
	ReadOnly  AccessMode = "read"
	ReadWrite AccessMode = "write"
)

type CreateOptions struct{ SessionID string }

type SessionInfo struct {
	SessionID     string
	Ref           SessionRef
	Codec         string
	Title         string
	ModelRef      string
	ModelIdentity string
	Turns         int
	CreatedAt     time.Time
	UpdatedAt     time.Time
	EventSequence uint64
	Path          string
	Error         string
}

type SessionPage struct {
	Sessions   []SessionInfo
	NextCursor string
}

type EventPage struct {
	Commits   []Commit
	Next      uint64
	Truncated bool
}

type SessionHandle interface {
	Read(context.Context, uint64, int) (EventPage, error)
	Append(context.Context, Batch) (Commit, error)
	Flush(context.Context) (DurableReceipt, error)
	Close(context.Context) error
}

// WritableSessionHandle is the live binding consumed by Session. Keeping this
// contract independent of the JSONL Store prevents the in-memory session and
// runtime registry from depending on a concrete persistence backend.
type WritableSessionHandle interface {
	SessionHandle
	SessionID() string
	Manifest() Manifest
	Snapshot() Snapshot
	AcceptedPage(context.Context, uint64, int) (EventPage, error)
	RecoverInterrupted(context.Context) (Commit, bool, error)
	Fork(context.Context, string, string, uint64) (Manifest, error)
	Export(context.Context, string) error
}

type SessionPersistence interface {
	Create(CreateOptions) (SessionHandle, error)
	Open(sessionID string, mode AccessMode) (SessionHandle, error)
	Stat(sessionID string) (SessionInfo, error)
	List(cursor string, limit int) (SessionPage, error)
}

// FilesystemPersistence owns a versioned sessions-v3 root.
type FilesystemPersistence struct{ Root string }

func NewFilesystemPersistence(root string) *FilesystemPersistence {
	return &FilesystemPersistence{Root: filepath.Clean(root)}
}

// RootForLegacyDir maps a host's legacy transcript catalog to the sibling
// final-format store. The mapping lives in the persistence package so Boot and
// controllers never derive a v3 identity from a transcript path.
func RootForLegacyDir(sessionDir string) string {
	dir := filepath.Clean(strings.TrimSpace(sessionDir))
	if dir == "." || dir == "" {
		return ""
	}
	if filepath.Base(dir) == "sessions" {
		return filepath.Join(filepath.Dir(dir), "sessions-v3")
	}
	return filepath.Join(dir, "sessions-v3")
}

func (p *FilesystemPersistence) Create(options CreateOptions) (SessionHandle, error) {
	id := strings.TrimSpace(options.SessionID)
	if id == "" {
		id = randomID()
	}
	if err := validateSessionID(id); err != nil {
		return nil, err
	}
	dir := filepath.Join(p.Root, filepath.Base(id))
	return CreateStore(dir, id)
}

func (p *FilesystemPersistence) Open(sessionID string, mode AccessMode) (SessionHandle, error) {
	id := strings.TrimSpace(sessionID)
	if err := validateSessionID(id); err != nil {
		return nil, err
	}
	dir := filepath.Join(p.Root, filepath.Base(id))
	if mode == ReadOnly {
		return openReadHandle(dir, id, filepath.Join(p.Root, ".query-cache", filepath.Base(id)))
	}
	if mode != ReadWrite {
		return nil, fmt.Errorf("sessionv3: unsupported access mode %q", mode)
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, id)
	} else if err != nil {
		return nil, err
	}
	return Open(dir, id)
}

func (p *FilesystemPersistence) Stat(sessionID string) (SessionInfo, error) {
	id := strings.TrimSpace(sessionID)
	if err := validateSessionID(id); err != nil {
		return SessionInfo{}, err
	}
	dir := filepath.Join(p.Root, filepath.Base(id))
	manifest, err := readManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return SessionInfo{}, fmt.Errorf("%w: %s", ErrSessionNotFound, id)
		}
		return SessionInfo{}, err
	}
	sequence, err := lastDurableSequenceWithCache(dir, filepath.Join(p.Root, ".query-cache", filepath.Base(id)))
	if err != nil {
		return SessionInfo{}, err
	}
	updatedAt := manifest.CreatedAt
	if stat, statErr := os.Stat(filepath.Join(dir, "events.jsonl")); statErr == nil && stat.ModTime().After(updatedAt) {
		updatedAt = stat.ModTime()
	}
	return SessionInfo{SessionID: manifest.SessionID, Codec: manifest.Codec, CreatedAt: manifest.CreatedAt, UpdatedAt: updatedAt, EventSequence: sequence, Path: dir}, nil
}

func (p *FilesystemPersistence) List(cursor string, limit int) (SessionPage, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return SessionPage{}, fmt.Errorf("sessionv3: list limit must be 1..100")
	}
	entries, err := os.ReadDir(p.Root)
	if os.IsNotExist(err) {
		return SessionPage{Sessions: []SessionInfo{}}, nil
	}
	if err != nil {
		return SessionPage{}, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") && entry.Name() > cursor {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	page := SessionPage{Sessions: []SessionInfo{}}
	for _, id := range ids {
		info, statErr := p.Stat(id)
		if statErr != nil {
			info = SessionInfo{SessionID: id, Path: filepath.Join(p.Root, id), Error: statErr.Error()}
		}
		if len(page.Sessions) == limit {
			page.NextCursor = page.Sessions[len(page.Sessions)-1].SessionID
			break
		}
		page.Sessions = append(page.Sessions, info)
	}
	return page, nil
}

func validateSessionID(id string) error {
	id = strings.TrimSpace(id)
	if !filepath.IsLocal(id) || id == "." || filepath.Base(id) != id || strings.ContainsAny(id, `/\\`) {
		return fmt.Errorf("sessionv3: invalid session id %q", id)
	}
	return nil
}

// Session is the live typed event log and its three projections. The
// persistence handle remains the only owner of physical durability.
type Session struct{ Handle WritableSessionHandle }

func (s *Session) AppendBatch(ctx context.Context, operationID string, events []Event) (Commit, error) {
	if s == nil || s.Handle == nil {
		return Commit{}, fmt.Errorf("sessionv3: nil session")
	}
	return s.Handle.Append(ctx, Batch{OperationID: operationID, Events: events})
}

func (s *Session) Flush(ctx context.Context) (DurableReceipt, error) {
	if s == nil || s.Handle == nil {
		return DurableReceipt{}, fmt.Errorf("sessionv3: nil session")
	}
	return s.Handle.Flush(ctx)
}

func (s *Session) Snapshot() Snapshot {
	if s == nil || s.Handle == nil {
		return Snapshot{PersistenceStatus: PersistenceFailed, PersistenceError: "nil session"}
	}
	return s.Handle.Snapshot()
}

func (s *Session) DeriveMessages() []provider.Message {
	return append([]provider.Message(nil), s.Snapshot().Projection.ModelMessages...)
}

func (s *Session) StateSnapshot() Snapshot {
	if s != nil && s.Handle != nil {
		if handle, ok := s.Handle.(interface{ StateSnapshot() Snapshot }); ok {
			return handle.StateSnapshot()
		}
	}
	return s.Snapshot()
}

// AcceptedPage returns the live accepted prefix, including events that have
// not crossed a durability checkpoint yet. Cold SessionHandle.Read continues
// to expose only durable records.
func (s *Store) AcceptedPage(ctx context.Context, offset uint64, limit int) (EventPage, error) {
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

type readHandle struct {
	dir      string
	cacheDir string
	mu       sync.Mutex
	closed   bool
}

func openReadHandle(dir, id string, cacheDirs ...string) (*readHandle, error) {
	cacheDir := dir
	if len(cacheDirs) > 0 && strings.TrimSpace(cacheDirs[0]) != "" {
		cacheDir = cacheDirs[0]
	}
	manifest, err := readManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, id)
		}
		return nil, err
	}
	if manifest.SessionID != id {
		return nil, fmt.Errorf("sessionv3: manifest belongs to %q", manifest.SessionID)
	}
	return &readHandle{dir: dir, cacheDir: cacheDir}, nil
}

func (h *readHandle) Read(ctx context.Context, offset uint64, limit int) (EventPage, error) {
	if h == nil {
		return EventPage{}, os.ErrClosed
	}
	h.mu.Lock()
	closed := h.closed
	dir, cacheDir := h.dir, h.cacheDir
	h.mu.Unlock()
	if closed {
		return EventPage{}, os.ErrClosed
	}
	return readCommitPageWithCache(ctx, dir, cacheDir, offset, limit)
}

func (h *readHandle) Append(context.Context, Batch) (Commit, error) {
	return Commit{}, ErrReadOnly
}
func (h *readHandle) Flush(context.Context) (DurableReceipt, error) {
	if h == nil {
		return DurableReceipt{}, os.ErrClosed
	}
	h.mu.Lock()
	closed := h.closed
	dir := h.dir
	h.mu.Unlock()
	if closed {
		return DurableReceipt{}, os.ErrClosed
	}
	sequence, err := lastDurableSequence(dir)
	return DurableReceipt{DurableSequence: sequence}, err
}
func (h *readHandle) Close(context.Context) error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	return nil
}

func (s *Store) Read(ctx context.Context, offset uint64, limit int) (EventPage, error) {
	if s == nil {
		return EventPage{}, os.ErrClosed
	}
	s.mu.Lock()
	closed := s.closed
	dir := s.dir
	s.mu.Unlock()
	if closed {
		return EventPage{}, os.ErrClosed
	}
	return readCommitPage(ctx, dir, offset, limit)
}

func readCommitPage(ctx context.Context, dir string, offset uint64, limit int) (EventPage, error) {
	return readCommitPageWithCache(ctx, dir, dir, offset, limit)
}

func readCommitPageWithCache(ctx context.Context, dir, cacheDir string, offset uint64, limit int) (EventPage, error) {
	if err := ctx.Err(); err != nil {
		return EventPage{}, err
	}
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 1000 {
		return EventPage{}, fmt.Errorf("sessionv3: read limit must be 1..1000 commits")
	}
	index, err := loadOrBuildSparseIndex(ctx, dir, cacheDir)
	if err != nil {
		return EventPage{}, err
	}
	page := EventPage{Commits: []Commit{}}
	if index.LastSequence <= offset {
		return page, nil
	}
	checkpoint := index.checkpoint(offset)
	file, err := os.Open(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		return EventPage{}, err
	}
	defer file.Close()
	err = scanCommitFile(file, checkpoint.Offset, checkpoint.FirstSequence, nil, func(_ int64, commit Commit) bool {
		if ctx.Err() != nil {
			return false
		}
		if commit.LastSequence() <= offset {
			return true
		}
		if len(page.Commits) == limit {
			page.Truncated = true
			return false
		}
		page.Commits = append(page.Commits, commit)
		page.Next = commit.LastSequence()
		return true
	})
	if err != nil {
		return EventPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return EventPage{}, err
	}
	return page, nil
}

func lastDurableSequence(dir string) (uint64, error) {
	return lastDurableSequenceWithCache(dir, dir)
}

func lastDurableSequenceWithCache(dir, cacheDir string) (uint64, error) {
	index, err := loadOrBuildSparseIndex(context.Background(), dir, cacheDir)
	return index.LastSequence, err
}

var _ SessionPersistence = (*FilesystemPersistence)(nil)
var _ SessionHandle = (*Store)(nil)
var _ SessionHandle = (*readHandle)(nil)
