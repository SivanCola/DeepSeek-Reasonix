package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"time"

	"reasonix/internal/historywork"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// DAG projections retain graph edges and source locations on disk. Bodies are
// decoded one entry at a time, never accumulated in the runtime message array.
// The authoritative graph and selected head are never modified by this reader.
type displayDAGLocation struct {
	Offset int64     `json:"offset"`
	Length int64     `json:"length"`
	ID     string    `json:"id"`
	Target string    `json:"target,omitempty"`
	At     time.Time `json:"at"`
}

func readDisplayDAGMessage(ctx context.Context, file *os.File, loc displayDAGLocation) (provider.Message, error) {
	var entry sessionDAGEntry
	reader := &historywork.Reader{Context: ctx, Source: io.NewSectionReader(file, loc.Offset, loc.Length)}
	if err := json.NewDecoder(reader).Decode(&entry); err != nil {
		return provider.Message{}, err
	}
	raw := entry.Msgs
	if loc.Target != "" {
		raw = entry.Targets[loc.Target]
	}
	var messages []provider.Message
	if err := json.Unmarshal(raw, &messages); err != nil {
		return provider.Message{}, err
	}
	if len(messages) != 1 {
		return provider.Message{}, ErrSessionDisplayReadModelDamaged
	}
	m := messages[0]
	if loc.ID != "" {
		m.ID = loc.ID
	}
	return m, ctx.Err()
}

func buildDAGDisplayPager(ctx context.Context, db *sql.DB, source, fingerprint, requestedHead string, checkpointSize int64) (result error) {
	// This is a private rebuild database with one connection. One transaction
	// avoids a durable fsync for each graph edge; SQLite spills its page cache.
	if _, err := db.ExecContext(ctx, "BEGIN"); err != nil {
		return err
	}
	defer func() {
		if result == nil {
			_, result = db.ExecContext(ctx, "COMMIT")
		}
		if result != nil {
			_, _ = db.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		}
	}()
	_, err := db.ExecContext(ctx, `CREATE TABLE dag_nodes(id TEXT PRIMARY KEY,parent TEXT NOT NULL,location BLOB NOT NULL);
	CREATE TABLE dag_overlays(kind TEXT NOT NULL,id TEXT NOT NULL,location BLOB NOT NULL,PRIMARY KEY(kind,id));
	CREATE TABLE dag_chain(position INTEGER PRIMARY KEY,id TEXT UNIQUE NOT NULL);
	CREATE TABLE dag_locations(position INTEGER PRIMARY KEY,location BLOB NOT NULL);`)
	if err != nil {
		return err
	}
	f, err := os.Open(store.SessionEventLog(source))
	if err != nil {
		return err
	}
	defer f.Close()
	state, err := scanDisplayDAGLocations(ctx, db, f, source)
	if err != nil {
		return err
	}
	headID := requestedHead
	if headID == "" {
		headID = state.selectedHead()
	}
	head := state.heads[headID]
	if head == nil {
		return fmt.Errorf("history head not found")
	}
	idx, err := projectDisplayDAGView(ctx, db, f, head, checkpointSize)
	if err != nil {
		return err
	}
	identity, known, err := SessionContentIdentity(source)
	if err != nil {
		return err
	}
	if known && requestedHead == "" {
		if idx.ContentDigest != identity.DigestHex {
			return fmt.Errorf("DAG selected history identity changed")
		}
		idx.Revision, idx.RevisionKnown = identity.Revision, identity.RevisionKnown
	}
	body, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO metadata VALUES('source',?),('header',?),('kind','dag'),('head',?)`, fingerprint, body, headID)
	return err
}

func storeDisplayDAGOverlay(ctx context.Context, db *sql.DB, f *os.File, kind, id string, loc displayDAGLocation) error {
	if _, err := readDisplayDAGMessage(ctx, f, loc); err != nil {
		return err
	}
	body, err := json.Marshal(loc)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT OR REPLACE INTO dag_overlays VALUES(?,?,?)`, kind, id, body)
	return err
}

func displayDAGSystem(ctx context.Context, db *sql.DB, key string) (displayDAGLocation, error) {
	var loc displayDAGLocation
	var raw []byte
	if err := db.QueryRowContext(ctx, `SELECT location FROM dag_overlays WHERE kind='system' AND id=?`, key).Scan(&raw); err != nil {
		return loc, err
	}
	err := json.Unmarshal(raw, &loc)
	return loc, err
}

func (p *DisplayPager) DAGMessages(lo, hi int) ([]provider.Message, error) {
	if lo < 0 || hi < lo || hi-lo > 500 {
		return nil, fmt.Errorf("display window exceeds page budget")
	}
	rows, err := p.DB.QueryContext(p.ctx, `SELECT location FROM dag_locations WHERE position>=? AND position<? ORDER BY position`, lo, hi)
	if err != nil {
		return nil, err
	}
	var locations []displayDAGLocation
	for rows.Next() {
		var body []byte
		var loc displayDAGLocation
		if err = rows.Scan(&body); err == nil {
			err = json.Unmarshal(body, &loc)
		}
		if err != nil {
			break
		}
		locations = append(locations, loc)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return nil, err
	}
	f, err := os.Open(store.SessionEventLog(p.source))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	result := make([]provider.Message, 0, len(locations))
	for _, loc := range locations {
		m, err := readDisplayDAGMessage(p.ctx, f, loc)
		if err != nil {
			return nil, err
		}
		if m.CreatedAt <= 0 && !loc.At.IsZero() {
			m.CreatedAt = loc.At.UnixMilli()
		}
		result = append(result, m)
	}
	return result, p.Validate()
}

func scanDisplayDAGLocations(ctx context.Context, db *sql.DB, f *os.File, source string) (*sessionDAGState, error) {
	decoder := json.NewDecoder(&historywork.Reader{Context: ctx, Source: f})
	state := newSessionDAGState(source)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		start := decoder.InputOffset()
		var e sessionDAGEntry
		if err := decoder.Decode(&e); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("DAG history: %w", ErrSessionDisplayReadModelDamaged)
		}
		if e.SchemaVersion != sessionDAGSchemaVersion {
			return nil, fmt.Errorf("unsupported DAG schema %d", e.SchemaVersion)
		}
		end := decoder.InputOffset()
		loc := displayDAGLocation{Offset: start, Length: end - start, ID: e.ID, At: e.At}
		switch e.Type {
		case sessionDAGTypeMessage:
			if e.ID == "" {
				return nil, ErrSessionDisplayReadModelDamaged
			}
			// Validate each record, including branches outside the selected view.
			if _, err := readDisplayDAGMessage(ctx, f, loc); err != nil {
				return nil, err
			}
			encoded, err := json.Marshal(loc)
			if err != nil {
				return nil, err
			}
			res, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO dag_nodes VALUES(?,?,?)`, e.ID, e.Parent, encoded)
			if err != nil {
				return nil, err
			}
			if count, _ := res.RowsAffected(); count == 0 {
				continue
			}
			h := state.headFor(e.Head, e.At)
			h.leaf, h.lastActivity, h.lastOffset = e.ID, e.At, end
		case sessionDAGTypePatch, sessionDAGTypeSystem, sessionDAGTypeRedact:
			if e.Type == sessionDAGTypePatch {
				var exists int
				if err := db.QueryRowContext(ctx, `SELECT 1 FROM dag_nodes WHERE id=?`, e.Target).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
					continue
				} else if err != nil {
					return nil, err
				}
				loc.ID = e.Target
			}
			if e.Type == sessionDAGTypeSystem {
				// Fork copies this immutable locator key, matching the native
				// replay's inherited system override without retaining its body.
				loc.ID = ""
				key := fmt.Sprint(start)
				state.headFor(e.Head, e.At).system = &provider.Message{ID: key}
				if err := storeDisplayDAGOverlay(ctx, db, f, "system", key, loc); err != nil {
					return nil, err
				}
			} else if e.Type == sessionDAGTypeRedact {
				for id := range e.Targets {
					loc.ID, loc.Target = id, id
					if err := storeDisplayDAGOverlay(ctx, db, f, "redact", id, loc); err != nil {
						return nil, err
					}
				}
			} else if err := storeDisplayDAGOverlay(ctx, db, f, "patch", e.Target, loc); err != nil {
				return nil, err
			}
		case sessionDAGTypeFork, sessionDAGTypeRewind, sessionDAGTypeSelect, sessionDAGTypeRename, sessionDAGTypeRetire,
			sessionDAGTypeTurnBegin, sessionDAGTypeTurnEnd, sessionDAGTypeCompaction:
			if !state.applyHeadMarker(e, end) {
				return nil, ErrSessionDisplayReadModelDamaged
			}
		case sessionDAGTypeLog:
			state.generation = e.Generation
		case sessionDAGTypeWriter, sessionDAGTypeCheckpoint:
		default:
			return nil, fmt.Errorf("unsupported DAG entry type %q", e.Type)
		}
	}
	return state, nil
}

type displayDAGProjectionWriter struct {
	ctx    context.Context
	db     *sql.DB
	file   *os.File
	index  SessionDisplayIndex
	hasher hash.Hash
	users  int
}

func (w *displayDAGProjectionWriter) append(loc displayDAGLocation) error {
	ctx, db, f := w.ctx, w.db, w.file
	idx, hasher := &w.index, w.hasher
	m, err := readDisplayDAGMessage(ctx, f, loc)
	if err != nil {
		return err
	}
	entry, turn := classifyDisplayIndexMessage(m, idx.MessageCount, loc.Offset, loc.Length, idx.AuthoredTurns)
	idx.AuthoredTurns = turn
	entryJSON, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	locationJSON, err := json.Marshal(loc)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO entries VALUES(?,?,?,?,?,?,?)`, entry.Index, entry.Offset, entry.Length, entry.AuthoredTurn, entry.Role, w.users, entryJSON); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO dag_locations VALUES(?,?)`, entry.Index, locationJSON); err != nil {
		return err
	}
	body, err := json.Marshal(messageForSessionIdentity(m))
	if err != nil {
		return err
	}
	hasher.Write(body)
	hasher.Write([]byte{'\n'})
	if m.Role == provider.RoleUser && !IsPinnedContextRevision(m) {
		w.users++
	}
	if entry.StartsTurn && idx.ListingPreview == "" {
		idx.ListingPreview = truncatePreview(previewProse(UserMessageText(m)))
	}
	idx.MessageCount++
	return nil
}

func projectDisplayDAGView(ctx context.Context, db *sql.DB, f *os.File, head *sessionDAGHead, checkpointSize int64) (SessionDisplayIndex, error) {
	count := 0
	for id := head.leaf; id != ""; {
		var parent string
		if err := db.QueryRowContext(ctx, `SELECT parent FROM dag_nodes WHERE id=?`, id).Scan(&parent); err != nil {
			return SessionDisplayIndex{}, fmt.Errorf("DAG history missing ancestor: %w", ErrSessionDisplayReadModelDamaged)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO dag_chain VALUES(?,?)`, count, id); err != nil {
			return SessionDisplayIndex{}, fmt.Errorf("DAG history cycle: %w", err)
		}
		count++
		id = parent
	}
	writer := &displayDAGProjectionWriter{ctx: ctx, db: db, file: f,
		index: SessionDisplayIndex{SchemaVersion: SessionDisplayIndexSchemaVersion, TranscriptSize: checkpointSize, ListingPreviewKnown: true}, hasher: sha256.New()}
	idx, hasher := &writer.index, writer.hasher

	for position := count - 1; position >= 0; position-- {
		var id string
		var raw []byte
		if err := db.QueryRowContext(ctx, `SELECT n.id,COALESCE(r.location,p.location,n.location) FROM dag_chain c JOIN dag_nodes n ON n.id=c.id
		LEFT JOIN dag_overlays p ON p.kind='patch' AND p.id=n.id LEFT JOIN dag_overlays r ON r.kind='redact' AND r.id=n.id WHERE c.position=?`, position).Scan(&id, &raw); err != nil {
			return SessionDisplayIndex{}, err
		}
		var loc displayDAGLocation
		if err := json.Unmarshal(raw, &loc); err != nil {
			return SessionDisplayIndex{}, err
		}
		loc.ID = id
		if position == count-1 && head.system != nil {
			m, err := readDisplayDAGMessage(ctx, f, loc)
			if err != nil {
				return SessionDisplayIndex{}, err
			}
			sys, err := displayDAGSystem(ctx, db, head.system.ID)
			if err != nil {
				return SessionDisplayIndex{}, err
			}
			if m.Role == provider.RoleSystem {
				sys.ID = id
				loc = sys
			} else if err := writer.append(sys); err != nil {
				return SessionDisplayIndex{}, err
			}
		}
		if err := writer.append(loc); err != nil {
			return SessionDisplayIndex{}, err
		}
	}
	if count == 0 && head.system != nil {
		loc, err := displayDAGSystem(ctx, db, head.system.ID)
		if err != nil {
			return SessionDisplayIndex{}, err
		}
		if err := writer.append(loc); err != nil {
			return SessionDisplayIndex{}, err
		}
	}
	idx.ContentDigest = fmt.Sprintf("%x", hasher.Sum(nil))
	return *idx, nil
}
