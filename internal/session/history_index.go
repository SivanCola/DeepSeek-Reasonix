package session

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/projectiondb"
	"reasonix/internal/provider"
	"reasonix/internal/sessioncontent"
)

const (
	historyIndexVersion     = 5
	HistoryPageDefaultLimit = 100
	HistoryPageMaxLimit     = 500
	HistoryPageMaxBytes     = 2 << 20
	historyIndexTxnEvents   = 512
	historyIndexTxnBytes    = 8 << 20
)

// PersistentMessage is the storage/query representation of a message. It is
// deliberately separate from provider.Message: provider DTOs are materialized
// only at model or compatibility boundaries.
type PersistentMessage struct {
	MessageID     string              `json:"messageId"`
	Position      int64               `json:"position"`
	Version       int                 `json:"version"`
	Role          string              `json:"role"`
	Preview       string              `json:"preview,omitempty"`
	EventSequence uint64              `json:"eventSequence"`
	VisibleTurn   int                 `json:"visibleTurn"`
	Inline        json.RawMessage     `json:"inline,omitempty"`
	ContentRef    *sessioncontent.Ref `json:"contentRef,omitempty"`
}

type MessageHistoryPage struct {
	Messages         []PersistentMessage `json:"messages"`
	SnapshotSequence uint64              `json:"snapshotSequence"`
	CoverageSequence uint64              `json:"coverageSequence"`
	Status           string              `json:"status"`
	TotalTurns       int                 `json:"totalTurns"`
	Generation       string              `json:"generation"`
	NextCursor       string              `json:"nextCursor,omitempty"`
	HasMore          bool                `json:"hasMore"`
}

// MessageLocation converts a stable message identity into a cursor for a
// fixed locator snapshot. It contains no message body.
type MessageLocation struct {
	Status           string `json:"status"`
	MessageID        string `json:"messageId,omitempty"`
	SnapshotSequence uint64 `json:"snapshotSequence"`
	CoverageSequence uint64 `json:"coverageSequence"`
	Generation       string `json:"generation,omitempty"`
	Position         int64  `json:"position,omitempty"`
	VisibleTurn      int    `json:"visibleTurn,omitempty"`
	Cursor           string `json:"cursor,omitempty"`
}

// HistoryPosition is the bounded display metadata for one message in a fixed
// durable snapshot. It deliberately excludes message bodies so callers can
// plan a window without pulling the transcript into memory.
type HistoryPosition struct {
	Position    int64         `json:"position"`
	VisibleTurn int           `json:"visibleTurn"`
	Role        provider.Role `json:"role"`
}

// HistoryShape describes the complete ordering of a fixed durable snapshot
// using only small per-message metadata. Message bodies are fetched later via
// HistoryWindow.
type HistoryShape struct {
	SnapshotSequence uint64            `json:"snapshotSequence"`
	Positions        []HistoryPosition `json:"positions"`
	TotalTurns       int               `json:"totalTurns"`
}

type historyCursor struct {
	SessionID        string `json:"sessionId"`
	StorageRevision  int    `json:"storageRevision"`
	SnapshotSequence uint64 `json:"snapshotSequence"`
	BeforePosition   int64  `json:"beforePosition"`
	Projection       int    `json:"projection"`
	Generation       string `json:"generation"`
}

type historyBuildState struct {
	nextPosition int64
	visibleTurn  int
	positions    map[string]int64
	turns        map[string]int
	versions     map[string]int
	tx           *sql.Tx
	statements   *historyBuildStatements
	transactions [][]any
	events       [][]any
	contentRefs  [][]any
	messages     [][]any
}

type historyPreparation struct {
	done chan struct{}
	err  error
}

type historyBuildStatements struct {
	clear  *sql.Stmt
	expire *sql.Stmt
}

func prepareHistoryBuildStatements(ctx context.Context, tx *sql.Tx) (*historyBuildStatements, error) {
	statements := &historyBuildStatements{}
	queries := []struct {
		target **sql.Stmt
		query  string
	}{
		{&statements.clear, `UPDATE messages SET current=0,valid_to=? WHERE current=1`},
		{&statements.expire, `UPDATE messages SET current=0,valid_to=? WHERE message_id=? AND current=1`},
	}
	for _, candidate := range queries {
		prepared, err := tx.PrepareContext(ctx, candidate.query)
		if err != nil {
			statements.close()
			return nil, err
		}
		*candidate.target = prepared
	}
	return statements, nil
}

func (s *historyBuildStatements) close() {
	if s == nil {
		return
	}
	for _, statement := range []*sql.Stmt{s.clear, s.expire} {
		if statement != nil {
			_ = statement.Close()
		}
	}
}

func historyIndexPath(root, sessionID string) string {
	return filepath.Join(root, ".query-cache", filepath.Base(sessionID), "history-locator-v1.sqlite")
}

var historyMigrations = []projectiondb.Migration{{Version: 1, Apply: func(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`CREATE TABLE metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE transactions (commit_id TEXT PRIMARY KEY, first_sequence INTEGER NOT NULL, last_sequence INTEGER NOT NULL, operation_id TEXT NOT NULL UNIQUE, operation_hash TEXT NOT NULL, turn_id TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE events (sequence INTEGER PRIMARY KEY, commit_id TEXT NOT NULL, event_id TEXT NOT NULL UNIQUE, kind TEXT NOT NULL, payload_digest TEXT NOT NULL DEFAULT '', payload_bytes INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE messages (message_id TEXT NOT NULL, version INTEGER NOT NULL, position INTEGER NOT NULL, event_sequence INTEGER NOT NULL, valid_to INTEGER NOT NULL DEFAULT 0, role TEXT NOT NULL, preview TEXT NOT NULL, inline BLOB, content_digest TEXT NOT NULL DEFAULT '', content_bytes INTEGER NOT NULL DEFAULT 0, content_index_digest TEXT NOT NULL DEFAULT '', current INTEGER NOT NULL, PRIMARY KEY(message_id, version))`,
		`CREATE UNIQUE INDEX messages_current_position ON messages(position) WHERE current=1`,
		`CREATE INDEX messages_current_id ON messages(message_id) WHERE current=1`,
		`CREATE TABLE content_refs (digest TEXT NOT NULL, bytes INTEGER NOT NULL, index_digest TEXT NOT NULL DEFAULT '', PRIMARY KEY(digest, bytes, index_digest))`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}}, {Version: 2, Apply: func(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE messages ADD COLUMN search_text TEXT NOT NULL DEFAULT ''`)
	return err
}}, {Version: 3, Apply: func(ctx context.Context, tx *sql.Tx) error {
	// Fixed-snapshot pages walk positions newest-to-oldest. Without this index,
	// SQLite scans and sorts the full message-body table for every page; on a
	// GiB history that turns a bounded result into seconds of disk traffic.
	_, err := tx.ExecContext(ctx, `CREATE INDEX messages_snapshot_position ON messages(position DESC, event_sequence, valid_to)`)
	return err
}}, {Version: 4, Apply: func(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE messages ADD COLUMN visible_turn INTEGER NOT NULL DEFAULT 0`)
	return err
}}, {Version: 5, Apply: func(ctx context.Context, tx *sql.Tx) error {
	// Revision 5 stops duplicating inline message bodies into search_text. The
	// rebuild metadata version forces old indexes through an atomic rebuild.
	_, err := tx.ExecContext(ctx, `SELECT 1`)
	return err
}}}

type SearchHistoryHit struct {
	MessageID     string `json:"messageId"`
	Position      int64  `json:"position"`
	Role          string `json:"role"`
	Preview       string `json:"preview"`
	EventSequence uint64 `json:"eventSequence"`
}

type SearchHistoryPage struct {
	Hits             []SearchHistoryHit `json:"hits"`
	SnapshotSequence uint64             `json:"snapshotSequence"`
	CoverageSequence uint64             `json:"coverageSequence"`
	Status           string             `json:"status"`
	NextCursor       string             `json:"nextCursor,omitempty"`
	HasMore          bool               `json:"hasMore"`
}

type searchHistoryCursor struct {
	SessionID        string `json:"sessionId"`
	StorageRevision  int    `json:"storageRevision"`
	SnapshotSequence uint64 `json:"snapshotSequence"`
	BeforePosition   int64  `json:"beforePosition"`
	Projection       int    `json:"projection"`
	QueryDigest      string `json:"queryDigest"`
	Generation       string `json:"generation"`
}

func (q *Query) HistoryPage(ctx context.Context, ref SessionRef, cursor string, limit int) (MessageHistoryPage, error) {
	if q == nil {
		return MessageHistoryPage{}, errors.New("session: nil query")
	}
	if err := ref.validate(q.hostID); err != nil {
		return MessageHistoryPage{}, err
	}
	filesystem, ok := q.persistence.(*FilesystemPersistence)
	if !ok {
		return MessageHistoryPage{}, errors.New("session: history index requires filesystem persistence")
	}
	if limit <= 0 {
		limit = HistoryPageDefaultLimit
	}
	limit = min(limit, HistoryPageMaxLimit)
	path := historyIndexPath(filesystem.Root, ref.SessionID)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		preparation := q.prepareHistoryLocator(filesystem, ref.SessionID, path)
		select {
		case <-preparation.done:
			if preparation.err != nil {
				return MessageHistoryPage{Status: "failed"}, preparation.err
			}
		default:
			return MessageHistoryPage{Messages: []PersistentMessage{}, Status: "preparing"}, nil
		}
	}
	lock := q.projectionLock("history", ref.SessionID)
	lock.Lock()
	err := ensureHistoryIndex(ctx, filesystem, ref.SessionID, path)
	lock.Unlock()
	if err != nil {
		return MessageHistoryPage{}, err
	}
	handle, err := projectiondb.Open(ctx, projectiondb.OpenOptions{Path: path, Migrations: historyMigrations, RequireDisk: true, MaxOpenConns: 1})
	if err != nil {
		return MessageHistoryPage{}, err
	}
	defer handle.DB.Close()
	metadata, err := readHistoryIndexMetadata(ctx, handle.DB)
	if err != nil {
		return MessageHistoryPage{}, err
	}
	snapshot := metadata.durableSequence
	// Empty cursor means the newest page. Subsequent cursors move toward older
	// positions while the snapshot sequence remains fixed.
	before := int64(^uint64(0) >> 1)
	if cursor != "" {
		parsed, err := decodeHistoryCursor(cursor)
		if err != nil {
			return MessageHistoryPage{}, err
		}
		if parsed.SessionID != ref.SessionID || parsed.StorageRevision != StorageRevision || parsed.Projection != historyIndexVersion || parsed.SnapshotSequence > snapshot || parsed.Generation != metadata.generation {
			return MessageHistoryPage{Messages: []PersistentMessage{}, Status: "stale_cursor", CoverageSequence: metadata.durableSequence, Generation: metadata.generation}, nil
		}
		snapshot = parsed.SnapshotSequence
		if parsed.BeforePosition <= 0 {
			return MessageHistoryPage{}, errors.New("session: invalid history cursor position")
		}
		before = parsed.BeforePosition
	}
	page := MessageHistoryPage{Messages: []PersistentMessage{}, SnapshotSequence: snapshot, CoverageSequence: metadata.durableSequence, Status: "ready", Generation: metadata.generation}
	if err := handle.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(visible_turn),0) FROM messages WHERE event_sequence<=? AND (valid_to=0 OR valid_to>?)`, snapshot, snapshot).Scan(&page.TotalTurns); err != nil {
		return MessageHistoryPage{}, err
	}
	rows, err := handle.DB.QueryContext(ctx, `SELECT message_id,position,version,role,preview,event_sequence,visible_turn,inline,content_digest,content_bytes,content_index_digest FROM messages WHERE position<? AND event_sequence<=? AND (valid_to=0 OR valid_to>?) ORDER BY position DESC LIMIT ?`, before, snapshot, snapshot, limit+1)
	if err != nil {
		return MessageHistoryPage{}, err
	}
	defer rows.Close()
	encodedBytes := 0
	storageGeneration := q.storageGeneration(ref.SessionID)
	for rows.Next() {
		var message PersistentMessage
		var inline []byte
		var digest, indexDigest string
		var contentBytes int64
		if err := rows.Scan(&message.MessageID, &message.Position, &message.Version, &message.Role, &message.Preview, &message.EventSequence, &message.VisibleTurn, &inline, &digest, &contentBytes, &indexDigest); err != nil {
			return MessageHistoryPage{}, err
		}
		if len(page.Messages) == limit {
			page.HasMore = true
			break
		}
		message.Inline = append(json.RawMessage(nil), inline...)
		if digest != "" {
			contentRef := sessioncontent.Ref{Digest: digest, Bytes: contentBytes, IndexDigest: indexDigest, IntegrityBlock: sessioncontent.IntegrityBlockBytes, MediaType: "application/json"}
			if contentBytes <= recentInlineBytes && encodedBytes+int(contentBytes) <= HistoryPageMaxBytes {
				body, readErr := contentStoreForSessionDir(filepath.Join(filesystem.Root, ref.SessionID)).ReadRange(ctx, contentRef, 0, contentBytes)
				if readErr != nil {
					return MessageHistoryPage{}, readErr
				}
				message.Inline = json.RawMessage(body)
			} else {
				message.ContentRef = &contentRef
				q.authorizeContentForGeneration(ref.SessionID, storageGeneration, digest, contentBytes, indexDigest)
			}
		}
		encoded, _ := json.Marshal(message)
		if len(page.Messages) > 0 && encodedBytes+len(encoded) > HistoryPageMaxBytes {
			page.HasMore = true
			break
		}
		encodedBytes += len(encoded)
		page.Messages = append(page.Messages, message)
	}
	if err := rows.Err(); err != nil {
		return MessageHistoryPage{}, err
	}
	if page.HasMore && len(page.Messages) > 0 {
		oldest := page.Messages[len(page.Messages)-1]
		page.NextCursor, err = encodeHistoryCursor(historyCursor{SessionID: ref.SessionID, StorageRevision: StorageRevision, SnapshotSequence: snapshot, BeforePosition: oldest.Position, Projection: historyIndexVersion, Generation: metadata.generation})
		if err != nil {
			return MessageHistoryPage{}, err
		}
	}
	slices.Reverse(page.Messages)
	return page, nil
}

// LocateMessage resolves a search hit or durable message id without scanning
// message bodies. A zero snapshot selects the locator's latest covered cut.
func (q *Query) LocateMessage(ctx context.Context, ref SessionRef, messageID string, snapshot uint64) (MessageLocation, error) {
	if q == nil {
		return MessageLocation{}, errors.New("session: nil query")
	}
	if err := ref.validate(q.hostID); err != nil {
		return MessageLocation{}, err
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return MessageLocation{}, errors.New("session: message id is required")
	}
	filesystem, ok := q.persistence.(*FilesystemPersistence)
	if !ok {
		return MessageLocation{}, errors.New("session: history locator requires filesystem persistence")
	}
	path := historyIndexPath(filesystem.Root, ref.SessionID)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		q.prepareHistoryLocator(filesystem, ref.SessionID, path)
		return MessageLocation{Status: "preparing", MessageID: messageID}, nil
	}
	lock := q.projectionLock("history", ref.SessionID)
	lock.Lock()
	err := ensureHistoryIndex(ctx, filesystem, ref.SessionID, path)
	lock.Unlock()
	if err != nil {
		return MessageLocation{}, err
	}
	handle, err := projectiondb.Open(ctx, projectiondb.OpenOptions{Path: path, Migrations: historyMigrations, RequireDisk: true, MaxOpenConns: 1})
	if err != nil {
		return MessageLocation{}, err
	}
	defer handle.DB.Close()
	metadata, err := readHistoryIndexMetadata(ctx, handle.DB)
	if err != nil {
		return MessageLocation{}, err
	}
	if snapshot == 0 {
		snapshot = metadata.durableSequence
	}
	location := MessageLocation{Status: "ready", MessageID: messageID, SnapshotSequence: snapshot, CoverageSequence: metadata.durableSequence, Generation: metadata.generation}
	if snapshot > metadata.durableSequence {
		location.Status = "preparing"
		return location, nil
	}
	err = handle.DB.QueryRowContext(ctx, `SELECT position,visible_turn FROM messages WHERE message_id=? AND event_sequence<=? AND (valid_to=0 OR valid_to>?) ORDER BY version DESC LIMIT 1`, messageID, snapshot, snapshot).Scan(&location.Position, &location.VisibleTurn)
	if errors.Is(err, sql.ErrNoRows) {
		location.Status = "not_found"
		return location, nil
	}
	if err != nil {
		return MessageLocation{}, err
	}
	location.Cursor, err = encodeHistoryCursor(historyCursor{SessionID: ref.SessionID, StorageRevision: StorageRevision, SnapshotSequence: snapshot, BeforePosition: location.Position + 1, Projection: historyIndexVersion, Generation: metadata.generation})
	return location, err
}

func (q *Query) prepareHistoryLocator(filesystem *FilesystemPersistence, sessionID, path string) *historyPreparation {
	q.historyMu.Lock()
	if current := q.historyBuilds[sessionID]; current != nil {
		q.historyMu.Unlock()
		return current
	}
	preparation := &historyPreparation{done: make(chan struct{})}
	q.historyBuilds[sessionID] = preparation
	q.historyMu.Unlock()
	go func() {
		select {
		case q.rebuildSlot <- struct{}{}:
			defer func() { <-q.rebuildSlot }()
		case <-q.rebuildCtx.Done():
			preparation.err = q.rebuildCtx.Err()
			close(preparation.done)
			return
		}
		lock := q.projectionLock("history", sessionID)
		lock.Lock()
		preparation.err = ensureHistoryIndex(q.rebuildCtx, filesystem, sessionID, path)
		lock.Unlock()
		close(preparation.done)
	}()
	return preparation
}

// HistoryShape returns the ordering and visible-turn boundaries for the
// current durable snapshot without materializing any message body.
func (q *Query) HistoryShape(ctx context.Context, ref SessionRef) (HistoryShape, error) {
	filesystem, path, err := q.prepareHistoryIndex(ctx, ref)
	if err != nil {
		return HistoryShape{}, err
	}
	_ = filesystem
	handle, err := projectiondb.Open(ctx, projectiondb.OpenOptions{Path: path, Migrations: historyMigrations, RequireDisk: true, MaxOpenConns: 1})
	if err != nil {
		return HistoryShape{}, err
	}
	defer handle.DB.Close()
	var snapshot uint64
	if err := scanMetadataUint(handle.DB.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='durable_sequence'`), &snapshot); err != nil {
		return HistoryShape{}, err
	}
	rows, err := handle.DB.QueryContext(ctx, `SELECT position,visible_turn,role FROM messages WHERE event_sequence<=? AND (valid_to=0 OR valid_to>?) ORDER BY position`, snapshot, snapshot)
	if err != nil {
		return HistoryShape{}, err
	}
	defer rows.Close()
	shape := HistoryShape{SnapshotSequence: snapshot, Positions: []HistoryPosition{}}
	for rows.Next() {
		var position HistoryPosition
		if err := rows.Scan(&position.Position, &position.VisibleTurn, &position.Role); err != nil {
			return HistoryShape{}, err
		}
		shape.Positions = append(shape.Positions, position)
		shape.TotalTurns = max(shape.TotalTurns, position.VisibleTurn)
	}
	if err := rows.Err(); err != nil {
		return HistoryShape{}, err
	}
	return shape, nil
}

// HistoryWindow materializes exactly [start,end) from a previously obtained
// durable snapshot. The snapshot must still be representable by the current
// projection; an append is allowed because version intervals retain the old
// view, while an index rebuild remains transparent.
func (q *Query) HistoryWindow(ctx context.Context, ref SessionRef, snapshot uint64, start, end int) ([]provider.Message, error) {
	filesystem, path, err := q.prepareHistoryIndex(ctx, ref)
	if err != nil {
		return nil, err
	}
	if start < 0 || end < start {
		return nil, errors.New("session: invalid history window")
	}
	handle, err := projectiondb.Open(ctx, projectiondb.OpenOptions{Path: path, Migrations: historyMigrations, RequireDisk: true, MaxOpenConns: 1})
	if err != nil {
		return nil, err
	}
	defer handle.DB.Close()
	var current uint64
	if err := scanMetadataUint(handle.DB.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='durable_sequence'`), &current); err != nil {
		return nil, err
	}
	if snapshot > current {
		return nil, errors.New("session: history snapshot is newer than durable state")
	}
	if start == end {
		return []provider.Message{}, nil
	}
	rows, err := handle.DB.QueryContext(ctx, `SELECT inline,content_digest,content_bytes,content_index_digest FROM messages WHERE position>? AND position<=? AND event_sequence<=? AND (valid_to=0 OR valid_to>?) ORDER BY position`, start, end, snapshot, snapshot)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	content := contentStoreForSessionDir(filepath.Join(filesystem.Root, ref.SessionID))
	messages := make([]provider.Message, 0, end-start)
	for rows.Next() {
		var inline []byte
		var digest, indexDigest string
		var contentBytes int64
		if err := rows.Scan(&inline, &digest, &contentBytes, &indexDigest); err != nil {
			return nil, err
		}
		body := json.RawMessage(inline)
		if digest != "" {
			body, err = resolveContentPayload(ctx, content, sessioncontent.Ref{Digest: digest, Bytes: contentBytes, IndexDigest: indexDigest, IntegrityBlock: sessioncontent.IntegrityBlockBytes, MediaType: "application/json"})
			if err != nil {
				return nil, err
			}
		}
		var message provider.Message
		if err := json.Unmarshal(body, &message); err != nil {
			return nil, fmt.Errorf("session: decode indexed message: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(messages) != end-start {
		return nil, fmt.Errorf("session: history window length %d, want %d", len(messages), end-start)
	}
	return messages, nil
}

func (q *Query) prepareHistoryIndex(ctx context.Context, ref SessionRef) (*FilesystemPersistence, string, error) {
	if q == nil {
		return nil, "", errors.New("session: nil query")
	}
	if err := ref.validate(q.hostID); err != nil {
		return nil, "", err
	}
	filesystem, ok := q.persistence.(*FilesystemPersistence)
	if !ok {
		return nil, "", errors.New("session: history index requires filesystem persistence")
	}
	path := historyIndexPath(filesystem.Root, ref.SessionID)
	lock := q.projectionLock("history", ref.SessionID)
	lock.Lock()
	err := ensureHistoryIndex(ctx, filesystem, ref.SessionID, path)
	lock.Unlock()
	if err != nil {
		return nil, "", err
	}
	return filesystem, path, nil
}

func (q *Query) ReadContent(ctx context.Context, ref SessionRef, contentRef sessioncontent.Ref, offset, length int64) ([]byte, error) {
	if q == nil {
		return nil, errors.New("session: nil query")
	}
	if err := ref.validate(q.hostID); err != nil {
		return nil, err
	}
	filesystem, ok := q.persistence.(*FilesystemPersistence)
	if !ok {
		return nil, errors.New("session: content reads require filesystem persistence")
	}
	if offset < 0 || length < 0 || length > 1<<20 || offset > contentRef.Bytes || length > contentRef.Bytes-offset {
		return nil, errors.New("session: invalid or oversized content range")
	}
	if !q.contentAuthorized(ref.SessionID, contentRef.Digest, contentRef.Bytes, contentRef.IndexDigest) {
		return nil, errors.New("session: content reference is not authorized for this session")
	}
	return contentStoreForSessionDir(filepath.Join(filesystem.Root, ref.SessionID)).ReadRange(ctx, contentRef, offset, length)
}

func ensureHistoryIndex(ctx context.Context, persistence *FilesystemPersistence, sessionID, path string) error {
	dir := filepath.Join(persistence.Root, sessionID)
	revision, err := revisionOfLog(dir)
	if err != nil {
		return err
	}
	if historyIndexCurrent(ctx, dir, path, sessionID, revision) {
		return nil
	}
	if updated, err := incrementHistoryIndex(ctx, dir, path, sessionID, revision); updated || err != nil {
		return err
	}
	return rebuildHistoryIndex(ctx, dir, path, sessionID, revision)
}

type historyIndexMetadata struct {
	sessionID       string
	logSize         int64
	storageRevision int
	projection      int
	durableSequence uint64
	viewSequence    uint64
	generation      string
}

func readHistoryIndexMetadata(ctx context.Context, db *sql.DB) (historyIndexMetadata, error) {
	values := map[string]string{}
	rows, err := db.QueryContext(ctx, `SELECT key,value FROM metadata WHERE key IN ('session_id','log_size','storage_revision','projection_version','durable_sequence','history_view_sequence','generation')`)
	if err != nil {
		return historyIndexMetadata{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return historyIndexMetadata{}, err
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return historyIndexMetadata{}, err
	}
	metadata := historyIndexMetadata{sessionID: values["session_id"], generation: values["generation"]}
	if _, err := fmt.Sscan(values["log_size"], &metadata.logSize); err != nil {
		return historyIndexMetadata{}, err
	}
	if _, err := fmt.Sscan(values["storage_revision"], &metadata.storageRevision); err != nil {
		return historyIndexMetadata{}, err
	}
	if _, err := fmt.Sscan(values["projection_version"], &metadata.projection); err != nil {
		return historyIndexMetadata{}, err
	}
	if _, err := fmt.Sscan(values["durable_sequence"], &metadata.durableSequence); err != nil {
		return historyIndexMetadata{}, err
	}
	if values["history_view_sequence"] != "" {
		if _, err := fmt.Sscan(values["history_view_sequence"], &metadata.viewSequence); err != nil {
			return historyIndexMetadata{}, err
		}
	}
	return metadata, nil
}

// incrementHistoryIndex advances only the complete transactions appended after
// the published coverage watermark. It returns updated=false when the existing
// file cannot be trusted as a base and must be rebuilt atomically.
func incrementHistoryIndex(ctx context.Context, dir, path, sessionID string, revision logRevision) (updated bool, result error) {
	if _, err := os.Stat(path); err != nil {
		return false, nil
	}
	handle, err := projectiondb.Open(ctx, projectiondb.OpenOptions{Path: path, Migrations: historyMigrations, RequireDisk: true, MaxOpenConns: 1})
	if err != nil {
		return false, nil
	}
	defer handle.DB.Close()
	metadata, err := readHistoryIndexMetadata(ctx, handle.DB)
	if err != nil || metadata.sessionID != sessionID || metadata.storageRevision != StorageRevision || metadata.projection != historyIndexVersion || metadata.logSize < 0 || metadata.logSize > revision.Size {
		return false, nil
	}
	if metadata.logSize == revision.Size {
		return true, nil
	}

	manifest, err := readManifest(filepath.Join(dir, "manifest.json"))
	if err != nil || manifest.Codec != Codec {
		return false, nil
	}
	log, err := os.Open(logPathForManifest(dir, manifest))
	if err != nil {
		return false, err
	}
	defer log.Close()

	state := historyBuildState{positions: map[string]int64{}, turns: map[string]int{}, versions: map[string]int{}}
	rows, err := handle.DB.QueryContext(ctx, `SELECT message_id,position,visible_turn,version FROM messages WHERE current=1`)
	if err != nil {
		return false, nil
	}
	for rows.Next() {
		var id string
		var position int64
		var visibleTurn, version int
		if err := rows.Scan(&id, &position, &visibleTurn, &version); err != nil {
			_ = rows.Close()
			return false, nil
		}
		state.positions[id], state.turns[id], state.versions[id] = position, visibleTurn, version
		state.nextPosition = max(state.nextPosition, position)
		state.visibleTurn = max(state.visibleTurn, visibleTurn)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return false, nil
	}

	tx, err := handle.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	state.tx = tx
	state.statements, err = prepareHistoryBuildStatements(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		return false, err
	}
	defer func() {
		state.statements.close()
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	content := contentStoreForSessionDir(dir)
	durable := metadata.durableSequence
	durableEnd := metadata.logSize
	viewSequence := metadata.viewSequence
	var buildErr error
	err = scanV4CommitFileRefs(ctx, log, metadata.logSize, metadata.durableSequence+1, content, nil, func(_ int64, commit Commit) bool {
		state.transactions = append(state.transactions, []any{commit.ID, commit.FirstSequence, commit.LastSequence(), commit.OperationID, commit.OperationHash, commit.TurnID, commit.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")})
		for _, event := range commit.Events {
			if event.Kind == "history/replace" {
				viewSequence = event.Sequence
			}
			digest := ""
			var contentBytes int64
			if event.PayloadRef != nil {
				digest, contentBytes = event.PayloadRef.Digest, event.PayloadRef.Bytes
				if err := insertContentRef(ctx, &state, *event.PayloadRef); err != nil {
					buildErr = err
					return false
				}
			}
			state.events = append(state.events, []any{event.Sequence, commit.ID, event.ID, event.Kind, digest, contentBytes})
			if err := indexMessageEvent(ctx, content, &state, event); err != nil {
				buildErr = err
				return false
			}
			durable = event.Sequence
		}
		durableEnd, _ = log.Seek(0, 1)
		return true
	})
	if err != nil || buildErr != nil {
		return false, errors.Join(err, buildErr)
	}
	if durableEnd == metadata.logSize {
		// An incomplete physical tail remains unpublished. A writer will preserve
		// and repair it before the next append.
		return true, nil
	}
	if err := flushHistoryBuildRows(ctx, tx, &state); err != nil {
		return false, err
	}
	generation, err := historyProjectionGeneration(dir, viewSequence)
	if err != nil {
		return false, err
	}
	values := map[string]string{
		"log_size": fmt.Sprint(durableEnd), "log_mtime_ns": fmt.Sprint(revision.ModTimeNS),
		"durable_sequence": fmt.Sprint(durable), "history_view_sequence": fmt.Sprint(viewSequence), "generation": generation,
	}
	for key, value := range values {
		if _, err := tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
			return false, err
		}
	}
	state.statements.close()
	state.statements = nil
	if err := tx.Commit(); err != nil {
		return false, err
	}
	tx = nil
	_, _ = handle.DB.ExecContext(ctx, `PRAGMA shrink_memory`)
	return true, nil
}

func historyIndexCurrent(ctx context.Context, dir, path, sessionID string, revision logRevision) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	handle, err := projectiondb.Open(ctx, projectiondb.OpenOptions{Path: path, Migrations: historyMigrations, RequireDisk: true, MaxOpenConns: 1})
	if err != nil {
		return false
	}
	defer handle.DB.Close()
	values := map[string]string{}
	rows, err := handle.DB.QueryContext(ctx, `SELECT key,value FROM metadata WHERE key IN ('session_id','log_size','log_mtime_ns','storage_revision','projection_version','generation')`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if rows.Scan(&key, &value) != nil {
			return false
		}
		values[key] = value
	}
	manifest, err := readStoredManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return false
	}
	identity, err := readStorageIdentity(dir, manifest)
	if err != nil || !strings.HasPrefix(values["generation"], identity.Generation+":") {
		return false
	}
	return values["session_id"] == sessionID && values["log_size"] == fmt.Sprint(revision.Size) && values["log_mtime_ns"] == fmt.Sprint(revision.ModTimeNS) && values["storage_revision"] == fmt.Sprint(StorageRevision) && values["projection_version"] == fmt.Sprint(historyIndexVersion)
}

func historyProjectionGeneration(dir string, viewSequence uint64) (string, error) {
	manifest, err := readStoredManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return "", err
	}
	identity, err := readStorageIdentity(dir, manifest)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%d", identity.Generation, viewSequence), nil
}

func rebuildHistoryIndex(ctx context.Context, dir, path, sessionID string, revision logRevision) error {
	manifest, err := readManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return err
	}
	log, err := os.Open(logPathForManifest(dir, manifest))
	if err != nil {
		return err
	}
	defer log.Close()
	content := contentStoreForSessionDir(dir)
	return projectiondb.Rebuild(ctx, projectiondb.OpenOptions{Path: path, Migrations: historyMigrations, RequireDisk: true, MaxOpenConns: 1, QuickCheck: true}, func(ctx context.Context, db *sql.DB) error {
		// Keep SQLite's derived-data working set explicit. The history database
		// may be many GiB, but neither its page cache nor temporary sort state
		// belongs in the runtime's cumulative memory footprint.
		// Rebuild writes an unpublished, disposable replacement beside the live
		// index. Avoid WAL and durability work for that private file; Rebuild
		// validates it before one atomic publish, and the event log remains the
		// durable source if a crash leaves or corrupts the temporary database.
		for _, pragma := range []string{
			`PRAGMA journal_mode=OFF`,
			`PRAGMA synchronous=OFF`,
			`PRAGMA locking_mode=EXCLUSIVE`,
			`PRAGMA cache_size=-8192`,
			`PRAGMA temp_store=FILE`,
		} {
			if _, err := db.ExecContext(ctx, pragma); err != nil {
				return err
			}
		}
		var tx *sql.Tx
		state := historyBuildState{positions: map[string]int64{}, turns: map[string]int{}, versions: map[string]int{}}
		defer func() {
			state.statements.close()
			if tx != nil {
				_ = tx.Rollback()
			}
		}()
		beginChunk := func() error {
			var err error
			tx, err = db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			state.tx = tx
			state.statements, err = prepareHistoryBuildStatements(ctx, tx)
			return err
		}
		if err := beginChunk(); err != nil {
			return err
		}
		var durable uint64
		var viewSequence uint64
		var buildErr error
		chunkEvents := 0
		var chunkBytes int64
		commitChunk := func() error {
			if err := flushHistoryBuildRows(ctx, tx, &state); err != nil {
				return err
			}
			state.statements.close()
			state.statements = nil
			if err := tx.Commit(); err != nil {
				return err
			}
			// modernc SQLite allocates its page cache on the Go heap. Release dirty
			// pages after each bounded transaction so a multi-GiB derived index does
			// not retain every completed chunk until the database closes.
			if _, err := db.ExecContext(ctx, `PRAGMA shrink_memory`); err != nil {
				return err
			}
			chunkEvents, chunkBytes = 0, 0
			return beginChunk()
		}
		err = scanV4CommitFileRefs(ctx, log, 0, 1, content, nil, func(_ int64, commit Commit) bool {
			state.transactions = append(state.transactions, []any{commit.ID, commit.FirstSequence, commit.LastSequence(), commit.OperationID, commit.OperationHash, commit.TurnID, commit.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")})
			for _, event := range commit.Events {
				if event.Kind == "history/replace" {
					viewSequence = event.Sequence
				}
				digest := ""
				var bytes int64
				if event.PayloadRef != nil {
					digest, bytes = event.PayloadRef.Digest, event.PayloadRef.Bytes
					if err := insertContentRef(ctx, &state, *event.PayloadRef); err != nil {
						buildErr = err
						return false
					}
				}
				state.events = append(state.events, []any{event.Sequence, commit.ID, event.ID, event.Kind, digest, bytes})
				if err := indexMessageEvent(ctx, content, &state, event); err != nil {
					buildErr = err
					return false
				}
				durable = event.Sequence
				chunkEvents++
				chunkBytes += int64(len(event.Payload))
				if event.PayloadRef != nil {
					chunkBytes += min(event.PayloadRef.Bytes, int64(historyIndexTxnBytes))
				}
				if chunkEvents >= historyIndexTxnEvents || chunkBytes >= historyIndexTxnBytes {
					if err := commitChunk(); err != nil {
						buildErr = err
						return false
					}
				}
			}
			return true
		})
		if err != nil {
			return err
		}
		if buildErr != nil {
			return buildErr
		}
		if err := flushHistoryBuildRows(ctx, tx, &state); err != nil {
			return err
		}
		generation, err := historyProjectionGeneration(dir, viewSequence)
		if err != nil {
			return err
		}
		metadata := map[string]string{"session_id": sessionID, "log_size": fmt.Sprint(revision.Size), "log_mtime_ns": fmt.Sprint(revision.ModTimeNS), "storage_revision": fmt.Sprint(StorageRevision), "projection_version": fmt.Sprint(historyIndexVersion), "durable_sequence": fmt.Sprint(durable), "history_view_sequence": fmt.Sprint(viewSequence), "generation": generation}
		for key, value := range metadata {
			if _, err := tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES(?,?)`, key, value); err != nil {
				return err
			}
		}
		state.statements.close()
		state.statements = nil
		err = tx.Commit()
		tx = nil
		return err
	})
}

func flushHistoryBuildRows(ctx context.Context, tx *sql.Tx, state *historyBuildState) error {
	if err := insertHistoryRows(ctx, tx, `INSERT INTO transactions(commit_id,first_sequence,last_sequence,operation_id,operation_hash,turn_id,created_at) VALUES `, 7, state.transactions); err != nil {
		return err
	}
	if err := insertHistoryRows(ctx, tx, `INSERT INTO events(sequence,commit_id,event_id,kind,payload_digest,payload_bytes) VALUES `, 6, state.events); err != nil {
		return err
	}
	if err := insertHistoryRows(ctx, tx, `INSERT OR IGNORE INTO content_refs(digest,bytes,index_digest) VALUES `, 3, state.contentRefs); err != nil {
		return err
	}
	if err := insertHistoryRows(ctx, tx, `INSERT INTO messages(message_id,version,position,event_sequence,valid_to,role,preview,inline,content_digest,content_bytes,content_index_digest,current,search_text,visible_turn) VALUES `, 14, state.messages); err != nil {
		return err
	}
	state.transactions = state.transactions[:0]
	state.events = state.events[:0]
	state.contentRefs = state.contentRefs[:0]
	state.messages = state.messages[:0]
	return nil
}

func insertHistoryRows(ctx context.Context, tx *sql.Tx, prefix string, columns int, rows [][]any) error {
	const rowsPerStatement = 128
	for start := 0; start < len(rows); start += rowsPerStatement {
		end := min(start+rowsPerStatement, len(rows))
		var query strings.Builder
		query.WriteString(prefix)
		args := make([]any, 0, (end-start)*columns)
		for rowIndex, row := range rows[start:end] {
			if len(row) != columns {
				return errors.New("session: invalid history index row width")
			}
			if rowIndex > 0 {
				query.WriteByte(',')
			}
			query.WriteByte('(')
			for column := range columns {
				if column > 0 {
					query.WriteByte(',')
				}
				query.WriteByte('?')
			}
			query.WriteByte(')')
			args = append(args, row...)
		}
		if _, err := tx.ExecContext(ctx, query.String(), args...); err != nil {
			return err
		}
	}
	return nil
}

func indexMessageEvent(ctx context.Context, content *sessioncontent.Store, state *historyBuildState, event Event) error {
	if event.Kind != "message/complete" && event.Kind != "message/upsert" && event.Kind != "history/replace" && event.Kind != "legacy/import" {
		return nil
	}
	payload := event.Payload
	if event.PayloadRef != nil {
		var err error
		payload, err = resolveContentPayload(ctx, content, *event.PayloadRef)
		if err != nil {
			return err
		}
	}
	switch event.Kind {
	case "message/complete", "message/upsert":
		var body struct {
			Message *provider.Message `json:"message"`
		}
		if err := strictPayload(payload, &body); err != nil || body.Message == nil {
			return damagedPayload(event, err)
		}
		return indexOneMessage(ctx, content, state, *body.Message, event.Sequence, event.Kind == "message/upsert")
	case "history/replace":
		var body struct {
			Messages []provider.Message `json:"messages"`
		}
		if err := strictPayload(payload, &body); err != nil || body.Messages == nil {
			return damagedPayload(event, err)
		}
		return replaceIndexedMessages(ctx, content, state, body.Messages, event.Sequence)
	case "legacy/import":
		var body struct {
			Messages []provider.Message `json:"messages"`
		}
		if err := strictPayload(payload, &body); err != nil || body.Messages == nil {
			return damagedPayload(event, err)
		}
		return replaceIndexedMessages(ctx, content, state, body.Messages, event.Sequence)
	}
	return nil
}

func replaceIndexedMessages(ctx context.Context, content *sessioncontent.Store, state *historyBuildState, messages []provider.Message, sequence uint64) error {
	if err := flushHistoryBuildRows(ctx, state.tx, state); err != nil {
		return err
	}
	if _, err := state.statements.clear.ExecContext(ctx, sequence); err != nil {
		return err
	}
	state.nextPosition = 0
	state.visibleTurn = 0
	state.positions = map[string]int64{}
	state.turns = map[string]int{}
	state.versions = map[string]int{}
	for _, message := range messages {
		if err := indexOneMessage(ctx, content, state, message, sequence, false); err != nil {
			return err
		}
	}
	return nil
}

func indexOneMessage(ctx context.Context, content *sessioncontent.Store, state *historyBuildState, message provider.Message, sequence uint64, upsert bool) error {
	id := strings.TrimSpace(message.ID)
	if id == "" {
		return errors.New("session: indexed message has no stable id")
	}
	position, exists := state.positions[id]
	visibleTurn := state.turns[id]
	if !exists {
		state.nextPosition++
		position = state.nextPosition
		state.positions[id] = position
		if agent.IsUserAuthoredTurnMessage(message) {
			state.visibleTurn++
		}
		visibleTurn = state.visibleTurn
		state.turns[id] = visibleTurn
	} else if !upsert {
		return fmt.Errorf("session: duplicate indexed message id %q", id)
	}
	version := state.versions[id] + 1
	state.versions[id] = version
	if exists {
		if err := flushHistoryBuildRows(ctx, state.tx, state); err != nil {
			return err
		}
		if _, err := state.statements.expire.ExecContext(ctx, sequence, id); err != nil {
			return err
		}
	}
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	ref, err := content.Put(ctx, bytes.NewReader(body), sessioncontent.Metadata{MediaType: "application/json"})
	if err != nil {
		return err
	}
	if err := insertContentRef(ctx, state, ref); err != nil {
		return err
	}
	state.messages = append(state.messages, []any{id, version, position, sequence, 0, string(message.Role), messagePreview(message), nil, ref.Digest, ref.Bytes, ref.IndexDigest, 1, "", visibleTurn})
	return nil
}

func messageSearchText(message provider.Message) string {
	parts := []string{message.Content, message.RawContent, message.ReasoningContent}
	return strings.Join(parts, "\n")
}

func insertContentRef(ctx context.Context, state *historyBuildState, ref sessioncontent.Ref) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state.contentRefs = append(state.contentRefs, []any{ref.Digest, ref.Bytes, ref.IndexDigest})
	return nil
}

func messagePreview(message provider.Message) string {
	preview := strings.TrimSpace(message.Content)
	if preview == "" {
		preview = strings.TrimSpace(message.RawContent)
	}
	runes := []rune(preview)
	if len(runes) > 240 {
		preview = string(runes[:240])
	}
	return preview
}

func encodeHistoryCursor(cursor historyCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeHistoryCursor(value string) (historyCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return historyCursor{}, errors.New("session: invalid history cursor")
	}
	var cursor historyCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return historyCursor{}, errors.New("session: invalid history cursor")
	}
	return cursor, nil
}

func encodeSearchHistoryCursor(cursor searchHistoryCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeSearchHistoryCursor(value string) (searchHistoryCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return searchHistoryCursor{}, errors.New("session: invalid history search cursor")
	}
	var cursor searchHistoryCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return searchHistoryCursor{}, errors.New("session: invalid history search cursor")
	}
	return cursor, nil
}

func scanMetadataUint(row *sql.Row, target *uint64) error {
	var value string
	if err := row.Scan(&value); err != nil {
		return err
	}
	_, err := fmt.Sscan(value, target)
	return err
}
