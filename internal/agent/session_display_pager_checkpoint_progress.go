package agent

import (
	"context"
	"database/sql"
	"encoding"
	"encoding/json"
	"errors"
	"hash"
)

type checkpointPagerProgress struct {
	Header SessionDisplayIndex `json:"header"`
	Users  int                 `json:"users"`
	Hash   []byte              `json:"hash"`
}

// The rebuild owner already fenced the entire source generation and holds its
// cross-process lock. Entries, offset, turn state and semantic digest always
// commit together. Nothing in this staging database is a readable generation.
func saveCheckpointPagerProgress(ctx context.Context, tx *sql.Tx, idx SessionDisplayIndex, users int, digest []byte) error {
	body, err := json.Marshal(checkpointPagerProgress{Header: idx, Users: users, Hash: digest})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO metadata VALUES('checkpoint_progress',?)`, string(body))
	return err
}

func restoreCheckpointPagerProgress(ctx context.Context, db *sql.DB, idx *SessionDisplayIndex, users *int, digest hash.Hash) error {
	var raw string
	err := db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='checkpoint_progress'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		// New staging is empty; a damaged/missing progress row cannot leave
		// entries that a fresh zero-offset build could accidentally reuse.
		_, err = db.ExecContext(ctx, `DELETE FROM entries; DELETE FROM metadata`)
		return err
	}
	if err != nil {
		return err
	}
	var progress checkpointPagerProgress
	valid := json.Unmarshal([]byte(raw), &progress) == nil && progress.Header.SchemaVersion == SessionDisplayIndexSchemaVersion &&
		progress.Header.Entries == nil && progress.Header.MessageCount >= 0 && progress.Header.TranscriptSize >= 0 &&
		progress.Users >= 0 && progress.Users <= progress.Header.MessageCount && progress.Header.AuthoredTurns >= 0 &&
		progress.Header.AuthoredTurns <= progress.Header.MessageCount
	var count int
	var end int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MAX(offset+length),0) FROM entries`).Scan(&count, &end); err != nil {
		return err
	}
	valid = valid && count == progress.Header.MessageCount && end == progress.Header.TranscriptSize
	if valid {
		valid = digest.(encoding.BinaryUnmarshaler).UnmarshalBinary(progress.Hash) == nil
	}
	if !valid {
		// Invalid disposable progress is rebuilt from the original source; it
		// never authorizes accepting a prefix or changing the authoritative file.
		digest.Reset()
		_, err := db.ExecContext(ctx, `DELETE FROM entries; DELETE FROM metadata`)
		return err
	}
	*idx, *users = progress.Header, progress.Users
	return nil
}
