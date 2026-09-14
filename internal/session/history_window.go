package session

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"reasonix/internal/projectiondb"
	"reasonix/internal/sessioncontent"
)

// History window reading (capability history-window-v1) pages a bounded
// window around an anchor: the newest page, a target message, a target turn,
// or an opaque continuation cursor — in either direction. It never walks
// pages from the newest position to reach a target: anchors resolve through
// the locator index. Protocol 7 HistoryPage stays unchanged for old clients.
const (
	HistoryWindowDefaultLimit = 32
	HistoryWindowMaxLimit     = 100

	historyWindowDirOlder = "older"
	historyWindowDirNewer = "newer"
)

// HistoryWindowRequest locates and pages one bounded window in a single
// round trip. Anchor selects the entry point; Direction picks which side of
// the anchor the page covers ("older" ends at the anchor, "newer" starts at
// it). Cursor anchors ignore MessageID/Turn.
type HistoryWindowRequest struct {
	Anchor    string `json:"anchor"`              // newest | message | turn | cursor
	MessageID string `json:"messageId,omitempty"` // anchor=message
	Turn      int    `json:"turn,omitempty"`      // anchor=turn
	Cursor    string `json:"cursor,omitempty"`    // anchor=cursor
	Direction string `json:"direction,omitempty"` // older (default) | newer
	Limit     int    `json:"limit,omitempty"`
}

// HistoryWindowPage is one bounded window of a fixed durable snapshot plus
// the cursors to keep reading in both directions.
type HistoryWindowPage struct {
	Messages         []PersistentMessage `json:"messages"`
	Status           string              `json:"status"` // preparing|ready|failed|stale_cursor|not_found
	SnapshotSequence uint64              `json:"snapshotSequence"`
	CoverageSequence uint64              `json:"coverageSequence"`
	Generation       string              `json:"generation,omitempty"`
	TotalTurns       int                 `json:"totalTurns"`
	HasOlder         bool                `json:"hasOlder"`
	HasNewer         bool                `json:"hasNewer"`
	OlderCursor      string              `json:"olderCursor,omitempty"`
	NewerCursor      string              `json:"newerCursor,omitempty"`
	AnchorMessageID  string              `json:"anchorMessageId,omitempty"`
	AnchorTurn       int                 `json:"anchorTurn,omitempty"`
}

// historyWindowCursor is the opaque cursor for window paging. Boundary is
// exclusive for older paging (positions < boundary) and inclusive for newer
// paging (positions >= boundary).
type historyWindowCursor struct {
	SessionID        string `json:"sessionId"`
	StorageRevision  int    `json:"storageRevision"`
	SnapshotSequence uint64 `json:"snapshotSequence"`
	Boundary         int64  `json:"boundary"`
	Direction        string `json:"direction"`
	Projection       int    `json:"projection"`
	Generation       string `json:"generation"`
}

func encodeHistoryWindowCursor(c historyWindowCursor) (string, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeHistoryWindowCursor(cursor string) (historyWindowCursor, error) {
	var c historyWindowCursor
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return historyWindowCursor{}, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return historyWindowCursor{}, err
	}
	return c, nil
}

// ReadHistoryWindow resolves the anchor against the locator index and returns one
// page. The snapshot stays fixed across a paging session: appends do not
// invalidate cursors (version intervals retain the old view), while a storage
// replacement or projection rebuild answers stale_cursor. The client
// re-anchors at most once on stale_cursor and keeps the current page after a
// second failure.
func (q *Query) ReadHistoryWindow(ctx context.Context, ref SessionRef, req HistoryWindowRequest) (HistoryWindowPage, error) {
	if q == nil {
		return HistoryWindowPage{}, errors.New("session: nil query")
	}
	if err := ref.validate(q.hostID); err != nil {
		return HistoryWindowPage{}, err
	}
	filesystem, ok := q.persistence.(*FilesystemPersistence)
	if !ok {
		return HistoryWindowPage{}, errors.New("session: history window requires filesystem persistence")
	}
	if req.Limit <= 0 {
		req.Limit = HistoryWindowDefaultLimit
	}
	req.Limit = min(req.Limit, HistoryWindowMaxLimit)
	if req.Direction != historyWindowDirNewer {
		req.Direction = historyWindowDirOlder
	}
	anchor := req.Anchor
	if anchor == "" {
		if req.Cursor != "" {
			anchor = "cursor"
		} else {
			anchor = "newest"
		}
	}
	switch anchor {
	case "newest", "message", "turn", "cursor":
	default:
		return HistoryWindowPage{}, fmt.Errorf("session: unknown history window anchor %q", req.Anchor)
	}

	path := historyIndexPath(filesystem.Root, ref.SessionID)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		preparation := q.prepareHistoryLocator(filesystem, ref.SessionID, path)
		select {
		case <-preparation.done:
			if preparation.err != nil {
				return HistoryWindowPage{Messages: []PersistentMessage{}, Status: "failed"}, preparation.err
			}
		default:
			return HistoryWindowPage{Messages: []PersistentMessage{}, Status: "preparing"}, nil
		}
	}
	lock := q.projectionLock("history", ref.SessionID)
	lock.Lock()
	err := ensureHistoryIndex(ctx, filesystem, ref.SessionID, path)
	lock.Unlock()
	if err != nil {
		return HistoryWindowPage{}, err
	}
	handle, err := projectiondb.Open(ctx, projectiondb.OpenOptions{Path: path, Migrations: historyMigrations, RequireDisk: true, MaxOpenConns: 1})
	if err != nil {
		return HistoryWindowPage{}, err
	}
	defer handle.DB.Close()
	metadata, err := readHistoryIndexMetadata(ctx, handle.DB)
	if err != nil {
		return HistoryWindowPage{}, err
	}

	page := HistoryWindowPage{
		Messages:         []PersistentMessage{},
		CoverageSequence: metadata.durableSequence,
		Generation:       metadata.generation,
		AnchorMessageID:  req.MessageID,
		AnchorTurn:       req.Turn,
	}
	// newest anchors always read the latest durable cut; cursor anchors pin
	// the snapshot they were issued under.
	boundary := int64(^uint64(0) >> 1)
	direction := req.Direction
	if anchor == "newest" {
		page.SnapshotSequence = metadata.durableSequence
	} else {
		var parsed historyWindowCursor
		var anchorPos int64
		switch anchor {
		case "cursor":
			parsed, err = decodeHistoryWindowCursor(req.Cursor)
			if err != nil {
				return HistoryWindowPage{}, err
			}
			if parsed.SessionID != ref.SessionID || parsed.StorageRevision != StorageRevision ||
				parsed.Projection != historyIndexVersion || parsed.Generation != metadata.generation ||
				(parsed.Direction != historyWindowDirOlder && parsed.Direction != historyWindowDirNewer) ||
				parsed.Boundary <= 0 {
				page.Status = "stale_cursor"
				return page, nil
			}
			if parsed.SnapshotSequence > metadata.durableSequence {
				page.Status = "stale_cursor"
				page.SnapshotSequence = metadata.durableSequence
				return page, nil
			}
			page.SnapshotSequence = parsed.SnapshotSequence
			boundary = parsed.Boundary
			direction = parsed.Direction
		case "message":
			page.SnapshotSequence = metadata.durableSequence
			anchorPos, err = resolveMessagePosition(ctx, handle.DB, req.MessageID, page.SnapshotSequence)
			if errors.Is(err, sql.ErrNoRows) {
				page.Status = "not_found"
				return page, nil
			}
			if err != nil {
				return HistoryWindowPage{}, err
			}
			boundary = anchorPos
			if direction == historyWindowDirOlder {
				boundary = anchorPos + 1 // the anchor message is the page's newest
			}
		case "turn":
			page.SnapshotSequence = metadata.durableSequence
			anchorPos, err = resolveTurnPosition(ctx, handle.DB, req.Turn, page.SnapshotSequence)
			if errors.Is(err, sql.ErrNoRows) {
				page.Status = "not_found"
				return page, nil
			}
			if err != nil {
				return HistoryWindowPage{}, err
			}
			boundary = anchorPos
			if direction == historyWindowDirOlder {
				boundary = anchorPos + 1
			}
		}
	}
	return q.readHistoryWindowPage(ctx, handle.DB, filesystem, ref, metadata, page.SnapshotSequence, boundary, direction, req.Limit)
}

func resolveMessagePosition(ctx context.Context, db *sql.DB, messageID string, snapshot uint64) (int64, error) {
	var position int64
	err := db.QueryRowContext(ctx,
		`SELECT position FROM messages WHERE message_id=? AND event_sequence<=? AND (valid_to=0 OR valid_to>?) ORDER BY version DESC LIMIT 1`,
		messageID, snapshot, snapshot).Scan(&position)
	return position, err
}

func resolveTurnPosition(ctx context.Context, db *sql.DB, turn int, snapshot uint64) (int64, error) {
	var position int64
	err := db.QueryRowContext(ctx,
		`SELECT MIN(position) FROM messages WHERE visible_turn=? AND event_sequence<=? AND (valid_to=0 OR valid_to>?)`,
		turn, snapshot, snapshot).Scan(&position)
	if err != nil {
		return 0, err
	}
	if position == 0 {
		// Zero positions never occur in the locator: MIN yielded NULL over no
		// rows only when the turn has no current messages — confirm to
		// distinguish not_found from a scan error.
		var exists bool
		if err := db.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM messages WHERE visible_turn=? AND event_sequence<=? AND (valid_to=0 OR valid_to>?))`,
			turn, snapshot, snapshot).Scan(&exists); err != nil {
			return 0, err
		}
		if !exists {
			return 0, sql.ErrNoRows
		}
	}
	return position, nil
}

// readHistoryWindowPage pages [boundary,∞) or (−∞,boundary) of a fixed
// snapshot in limit-sized steps, inlining small bodies and authorizing
// content-range credentials for referenced ones — the same body budget as
// protocol 7 pages.
func (q *Query) readHistoryWindowPage(ctx context.Context, db *sql.DB, filesystem *FilesystemPersistence, ref SessionRef, metadata historyIndexMetadata, snapshot uint64, boundary int64, direction string, limit int) (HistoryWindowPage, error) {
	page := HistoryWindowPage{
		Messages:         []PersistentMessage{},
		Status:           "ready",
		SnapshotSequence: snapshot,
		CoverageSequence: metadata.durableSequence,
		Generation:       metadata.generation,
	}
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(visible_turn),0) FROM messages WHERE event_sequence<=? AND (valid_to=0 OR valid_to>?)`, snapshot, snapshot).Scan(&page.TotalTurns); err != nil {
		return HistoryWindowPage{}, err
	}
	var rows *sql.Rows
	var err error
	if direction == historyWindowDirNewer {
		rows, err = db.QueryContext(ctx, `SELECT message_id,position,version,role,preview,event_sequence,visible_turn,inline,content_digest,content_bytes,content_index_digest FROM messages WHERE position>=? AND event_sequence<=? AND (valid_to=0 OR valid_to>?) ORDER BY position ASC LIMIT ?`, boundary, snapshot, snapshot, limit+1)
	} else {
		rows, err = db.QueryContext(ctx, `SELECT message_id,position,version,role,preview,event_sequence,visible_turn,inline,content_digest,content_bytes,content_index_digest FROM messages WHERE position<? AND event_sequence<=? AND (valid_to=0 OR valid_to>?) ORDER BY position DESC LIMIT ?`, boundary, snapshot, snapshot, limit+1)
	}
	if err != nil {
		return HistoryWindowPage{}, err
	}
	defer rows.Close()
	encodedBytes := 0
	storageGeneration := q.storageGeneration(ref.SessionID)
	scanned := 0
	for rows.Next() {
		var message PersistentMessage
		var inline []byte
		var digest, indexDigest string
		var contentBytes int64
		if err := rows.Scan(&message.MessageID, &message.Position, &message.Version, &message.Role, &message.Preview, &message.EventSequence, &message.VisibleTurn, &inline, &digest, &contentBytes, &indexDigest); err != nil {
			return HistoryWindowPage{}, err
		}
		scanned++
		if len(page.Messages) == limit {
			break
		}
		message.Inline = append(json.RawMessage(nil), inline...)
		if digest != "" {
			contentRef := sessioncontent.Ref{Digest: digest, Bytes: contentBytes, IndexDigest: indexDigest, IntegrityBlock: sessioncontent.IntegrityBlockBytes, MediaType: "application/json"}
			if contentBytes <= recentInlineBytes && encodedBytes+int(contentBytes) <= HistoryPageMaxBytes {
				body, readErr := contentStoreForSessionDir(filepath.Join(filesystem.Root, ref.SessionID)).ReadRange(ctx, contentRef, 0, contentBytes)
				if readErr != nil {
					return HistoryWindowPage{}, readErr
				}
				message.Inline = json.RawMessage(body)
			} else {
				message.ContentRef = &contentRef
				q.authorizeContentForGeneration(ref.SessionID, storageGeneration, digest, contentBytes, indexDigest)
			}
		}
		encoded, _ := json.Marshal(message)
		if len(page.Messages) > 0 && encodedBytes+len(encoded) > HistoryPageMaxBytes {
			break
		}
		encodedBytes += len(encoded)
		page.Messages = append(page.Messages, message)
	}
	if err := rows.Err(); err != nil {
		return HistoryWindowPage{}, err
	}
	hasMoreBeyond := scanned > len(page.Messages)
	if direction == historyWindowDirNewer {
		page.HasNewer = hasMoreBeyond
		page.HasOlder = boundary > 1
		if page.HasNewer && len(page.Messages) > 0 {
			last := page.Messages[len(page.Messages)-1]
			page.NewerCursor, err = encodeHistoryWindowCursor(historyWindowCursor{SessionID: ref.SessionID, StorageRevision: StorageRevision, SnapshotSequence: snapshot, Boundary: last.Position + 1, Direction: historyWindowDirNewer, Projection: historyIndexVersion, Generation: metadata.generation})
			if err != nil {
				return HistoryWindowPage{}, err
			}
		}
		if page.HasOlder {
			page.OlderCursor, err = encodeHistoryWindowCursor(historyWindowCursor{SessionID: ref.SessionID, StorageRevision: StorageRevision, SnapshotSequence: snapshot, Boundary: boundary, Direction: historyWindowDirOlder, Projection: historyIndexVersion, Generation: metadata.generation})
			if err != nil {
				return HistoryWindowPage{}, err
			}
		}
		// Newer pages were collected oldest-first: already display order.
		return page, nil
	}
	// Older paging: the collected messages are newest-first and reversed for
	// display; continuation cursors cover both directions.
	page.HasOlder = hasMoreBeyond
	if page.HasOlder && len(page.Messages) > 0 {
		oldest := page.Messages[len(page.Messages)-1]
		page.OlderCursor, err = encodeHistoryWindowCursor(historyWindowCursor{SessionID: ref.SessionID, StorageRevision: StorageRevision, SnapshotSequence: snapshot, Boundary: oldest.Position, Direction: historyWindowDirOlder, Projection: historyIndexVersion, Generation: metadata.generation})
		if err != nil {
			return HistoryWindowPage{}, err
		}
	}
	// The locator opens at most one connection: release this result set
	// before issuing the existence probe.
	rows.Close()
	var newer bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM messages WHERE position>=? AND event_sequence<=? AND (valid_to=0 OR valid_to>?))`, boundary, snapshot, snapshot).Scan(&newer); err != nil {
		return HistoryWindowPage{}, err
	}
	page.HasNewer = newer
	if page.HasNewer {
		page.NewerCursor, err = encodeHistoryWindowCursor(historyWindowCursor{SessionID: ref.SessionID, StorageRevision: StorageRevision, SnapshotSequence: snapshot, Boundary: boundary, Direction: historyWindowDirNewer, Projection: historyIndexVersion, Generation: metadata.generation})
		if err != nil {
			return HistoryWindowPage{}, err
		}
	}
	slices.Reverse(page.Messages)
	return page, nil
}
