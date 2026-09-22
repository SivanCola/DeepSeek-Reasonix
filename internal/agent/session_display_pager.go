package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"reasonix/internal/fileops"
	"reasonix/internal/historywork"
	"reasonix/internal/projectiondb"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// DisplayPager is a disposable disk projection of exact legacy byte offsets.
// It never converts or rewrites the source transcript. Header contains no
// Entries; individual positions are queried through SQLite's bounded cache.
type DisplayPager struct {
	DB            *sql.DB
	Header        SessionDisplayIndex
	Built         bool
	DAG           bool
	ctx           context.Context
	source        string
	sourceInfo    os.FileInfo
	eventInfo     os.FileInfo
	sourceVersion fileops.Version
	eventVersion  fileops.Version
}

var ErrDisplaySourceChanged = errors.New("display source changed")
var ErrDisplayFormatUnsupported = errors.New("display format requires compatibility reader")

var displayPagerMigrations = []projectiondb.Migration{{Version: 1, Apply: func(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL);
	CREATE TABLE entries(position INTEGER PRIMARY KEY,offset INTEGER NOT NULL,length INTEGER NOT NULL,turn INTEGER NOT NULL,role TEXT NOT NULL,user_before INTEGER NOT NULL,entry BLOB NOT NULL);
	CREATE INDEX entries_turn ON entries(turn,position);`)
	return err
}}}

func OpenDisplayPager(ctx context.Context, source, cachePath string, heads ...string) (*DisplayPager, error) {
	head := ""
	if len(heads) > 0 {
		head = heads[0]
	}
	return openDisplayPager(ctx, source, cachePath, head, false)
}

func openDisplayPager(ctx context.Context, source, cachePath, head string, forceSource bool) (*DisplayPager, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Stat(source)
	if err != nil {
		return nil, err
	}
	identity, known, err := SessionContentIdentity(source)
	if err != nil {
		return nil, err
	}
	indexPath := store.SessionDisplayIndex(source)
	indexInfo, err := os.Stat(indexPath)
	plain := os.IsNotExist(err)
	if err != nil && !plain {
		return nil, err
	}
	// Equal timestamps are ambiguous. Leave them to the explicit authoritative
	// preparation path, never certify stale offsets from size alone.
	if !plain && (forceSource || !known || !indexInfo.ModTime().After(SessionContentModTime(source))) {
		plain = true
	}
	sourceTarget, sourceVersion := fileops.DiskSnapshot(source, info)
	fingerprint := fmt.Sprintf("%s:%s", sourceTarget.Key, sourceVersion)
	var eventInfo os.FileInfo
	var eventVersion fileops.Version
	dag := false
	hasDAG := false
	if eventInfo, err = os.Stat(store.SessionEventLog(source)); err == nil && eventInfo.Size() > 0 {
		f, openErr := os.Open(store.SessionEventLog(source))
		if openErr != nil {
			return nil, openErr
		}
		schema, _, known := probeSessionEventHeader(&historywork.Reader{Context: ctx, Source: f})
		_ = f.Close()
		hasDAG = known && schema == sessionDAGSchemaVersion
		dag = hasDAG && (plain || head != "" || forceSource)
		eventTarget, version := fileops.DiskSnapshot(store.SessionEventLog(source), eventInfo)
		eventVersion = version
		fingerprint += fmt.Sprintf(":event:%s:%s", eventTarget.Key, eventVersion)
		if dag {
			fingerprint += fmt.Sprintf(":dag:%d:%d:%s", eventInfo.Size(), eventInfo.ModTime().UnixNano(), head)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	} else {
		// Missing and empty logs carry no authority over a checkpoint. Record
		// that absence explicitly so Validate can detect a newly written log.
		eventInfo = nil
	}
	if head != "" && !dag {
		return nil, errors.New("requested branch has no DAG source")
	}
	if dag {
		plain = false
	}
	if plain {
		// A checkpoint is only authoritative in the absence of an event log.
		// Event/DAG readers must establish the selected view before indexing.
		if logInfo, logErr := os.Stat(store.SessionEventLog(source)); logErr == nil && logInfo.Size() > 0 {
			return nil, ErrDisplayFormatUnsupported
		} else if logErr != nil && !os.IsNotExist(logErr) {
			return nil, logErr
		}
		fingerprint += ":checkpoint"
	} else if !dag {
		fingerprint += fmt.Sprintf(":%d:%d", indexInfo.Size(), indexInfo.ModTime().UnixNano())
	}
	opts := projectiondb.OpenOptions{Path: cachePath, Migrations: displayPagerMigrations, RequireDisk: true, MaxOpenConns: 1}
	handle, err := projectiondb.Open(ctx, opts)
	if err != nil {
		return nil, err
	}
	var stored string
	err = handle.DB.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='source'`).Scan(&stored)
	built := err != nil || stored != fingerprint
	if built {
		_ = handle.DB.Close()
		err = projectiondb.Rebuild(ctx, opts, func(ctx context.Context, db *sql.DB) error {
			if dag {
				return buildDAGDisplayPager(ctx, db, source, fingerprint, head, info.Size())
			}
			if plain {
				return buildCheckpointDisplayPager(ctx, db, source, fingerprint)
			}
			return importDisplayPager(ctx, db, indexPath, fingerprint)
		})
		if err != nil {
			if !dag && !plain && ctx.Err() == nil {
				return openDisplayPager(ctx, source, cachePath, head, true)
			}
			return nil, err
		}
		handle, err = projectiondb.Open(ctx, opts)
		if err != nil {
			return nil, err
		}
	}
	p := &DisplayPager{DB: handle.DB, ctx: ctx, source: source, sourceInfo: info, eventInfo: eventInfo, sourceVersion: sourceVersion, eventVersion: eventVersion, DAG: dag, Built: built && (plain || dag)}
	var header string
	if err := p.DB.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='header'`).Scan(&header); err != nil {
		p.Close()
		return nil, err
	}
	if err := json.Unmarshal([]byte(header), &p.Header); err != nil {
		p.Close()
		return nil, err
	}
	if p.Header.TranscriptSize != info.Size() || known && head == "" && (p.Header.ContentDigest != identity.DigestHex || p.Header.RevisionKnown != identity.RevisionKnown || p.Header.RevisionKnown && p.Header.Revision != identity.Revision) {
		p.Close()
		if !dag && !plain {
			return openDisplayPager(ctx, source, cachePath, head, true)
		}
		return nil, ErrDisplaySourceChanged
	}
	if err := p.Validate(); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

func (p *DisplayPager) Close() error { return p.DB.Close() }

// WithContext borrows the projection for a single caller. Only its preparation
// owner closes the database; canceling a read cannot cancel other readers.
func (p *DisplayPager) WithContext(ctx context.Context) *DisplayPager {
	copy := *p
	copy.ctx = ctx
	return &copy
}

func (p *DisplayPager) Validate() error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	st, err := os.Stat(p.source)
	if err != nil {
		return err
	}
	if !os.SameFile(st, p.sourceInfo) || st.Size() != p.sourceInfo.Size() || !st.ModTime().Equal(p.sourceInfo.ModTime()) {
		return ErrDisplaySourceChanged
	}
	if _, version := fileops.DiskSnapshot(p.source, st); version != p.sourceVersion {
		return ErrDisplaySourceChanged
	}
	if p.eventInfo != nil {
		current, err := os.Stat(store.SessionEventLog(p.source))
		if err != nil {
			return err
		}
		if !os.SameFile(current, p.eventInfo) || current.Size() != p.eventInfo.Size() || !current.ModTime().Equal(p.eventInfo.ModTime()) {
			return ErrDisplaySourceChanged
		}
		if _, version := fileops.DiskSnapshot(store.SessionEventLog(p.source), current); version != p.eventVersion {
			return ErrDisplaySourceChanged
		}
	} else {
		current, err := os.Stat(store.SessionEventLog(p.source))
		if err == nil && current.Size() > 0 {
			return ErrDisplaySourceChanged
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (p *DisplayPager) Entry(position int) (DisplayIndexEntry, error) {
	var entry DisplayIndexEntry
	var body []byte
	if err := p.DB.QueryRowContext(p.ctx, `SELECT entry FROM entries WHERE position=?`, position).Scan(&body); err != nil {
		return entry, err
	}
	err := json.Unmarshal(body, &entry)
	return entry, err
}

func (p *DisplayPager) Entries(lo, hi int) ([]DisplayIndexEntry, error) {
	if lo < 0 || hi < lo || hi-lo > 500 {
		return nil, errors.New("display window exceeds page budget")
	}
	rows, err := p.DB.QueryContext(p.ctx, `SELECT entry FROM entries WHERE position>=? AND position<? ORDER BY position`, lo, hi)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]DisplayIndexEntry, 0, hi-lo)
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var e DisplayIndexEntry
		if err := json.Unmarshal(b, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (p *DisplayPager) UsersBefore(position int) (int, error) {
	var count int
	err := p.DB.QueryRowContext(p.ctx, `SELECT user_before FROM entries WHERE position=?`, position).Scan(&count)
	return count, err
}

// Import the existing JSON index one entry at a time. A corrupt or cancelled
// build never replaces the last published projection.
func importDisplayPager(ctx context.Context, db *sql.DB, indexPath, fingerprint string) error {
	f, err := os.Open(indexPath)
	if err != nil {
		return err
	}
	defer f.Close()
	decoder := json.NewDecoder(&historywork.Reader{Context: ctx, Source: f})
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("invalid display index object")
	}
	header := map[string]json.RawMessage{}
	count, users := 0, 0
	var offset int64
	sawEntries := false
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok {
			return errors.New("invalid display index field")
		}
		if name != "entries" {
			var raw json.RawMessage
			if err := decoder.Decode(&raw); err != nil {
				return err
			}
			header[name] = raw
			continue
		}
		if sawEntries {
			return errors.New("duplicate display entries")
		}
		sawEntries = true
		token, err := decoder.Token()
		if err != nil || token != json.Delim('[') {
			return errors.New("invalid display entries")
		}
		for decoder.More() {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			for batch := 0; batch < historywork.BatchEntries && decoder.More(); batch++ {
				var e DisplayIndexEntry
				if err = decoder.Decode(&e); err != nil {
					break
				}
				if e.Index != count || e.Offset != offset || e.Length <= 0 {
					err = errors.New("invalid display offset chain")
					break
				}
				body, marshalErr := json.Marshal(e)
				if marshalErr != nil {
					err = marshalErr
					break
				}
				_, err = tx.ExecContext(ctx, `INSERT INTO entries VALUES(?,?,?,?,?,?,?)`, count, e.Offset, e.Length, e.AuthoredTurn, e.Role, users, body)
				if err != nil {
					break
				}
				if e.Role == provider.RoleUser && !e.PinnedContextRevision {
					users++
				}
				count++
				offset += e.Length
			}
			if err != nil {
				_ = tx.Rollback()
				return err
			}
			if err := tx.Commit(); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing display index data")
	}
	body, err := json.Marshal(header)
	if err != nil {
		return err
	}
	var idx SessionDisplayIndex
	if err := json.Unmarshal(body, &idx); err != nil {
		return err
	}
	if !sawEntries || idx.SchemaVersion != SessionDisplayIndexSchemaVersion || idx.MessageCount != count || idx.TranscriptSize != offset {
		return errors.New("display index header does not cover entries")
	}
	_, err = db.ExecContext(ctx, `INSERT INTO metadata VALUES('source',?),('header',?)`, fingerprint, string(body))
	return err
}
