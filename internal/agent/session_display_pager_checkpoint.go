package agent

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"reasonix/internal/historywork"
	"reasonix/internal/provider"
)

// Build an offset-only projection for a checkpoint without publishing or
// repairing any session sidecar. At most one message and one SQLite batch are
// retained. Digest validation happens before the disposable index is published.
func buildCheckpointDisplayPager(ctx context.Context, db *sql.DB, source, fingerprint string) error {
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	reader := bufio.NewReaderSize(&historywork.Reader{Context: ctx, Source: f}, historywork.ReadChunk)
	hash := sha256.New()
	idx := SessionDisplayIndex{SchemaVersion: SessionDisplayIndexSchemaVersion, ListingPreviewKnown: true}
	users := 0
	done := false
	for !done {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for batch := 0; batch < historywork.BatchEntries; batch++ {
			line, readErr := readSessionDisplayIndexLine(reader)
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				err = readErr
				break
			}
			if len(line) > 0 {
				var message provider.Message
				if err = json.Unmarshal(line, &message); err != nil {
					break
				}
				if message.Role == "" {
					err = errors.New("checkpoint message has no role")
					// Ancient event rows use kind/type instead of provider roles.
					// Classify only the first record as an unsupported format;
					// a foreign record after valid messages is damaged content.
					if idx.MessageCount == 0 {
						var event struct {
							Kind string `json:"kind"`
							Type string `json:"type"`
						}
						if json.Unmarshal(line, &event) == nil && (event.Kind != "" || event.Type != "") {
							err = ErrDisplayFormatUnsupported
						}
					}
					break
				}
				var entry DisplayIndexEntry
				entry, idx.AuthoredTurns = classifyDisplayIndexMessage(message, idx.MessageCount, idx.TranscriptSize, int64(len(line)), idx.AuthoredTurns)
				var encoded []byte
				encoded, err = json.Marshal(entry)
				if err != nil {
					break
				}
				_, err = tx.ExecContext(ctx, `INSERT INTO entries VALUES(?,?,?,?,?,?,?)`, entry.Index, entry.Offset, entry.Length, entry.AuthoredTurn, entry.Role, users, encoded)
				if err != nil {
					break
				}
				encoded, err = json.Marshal(messageForSessionIdentity(message))
				if err != nil {
					break
				}
				_, _ = hash.Write(encoded)
				_, _ = hash.Write([]byte{'\n'})
				if entry.Role == provider.RoleUser && !entry.PinnedContextRevision {
					users++
				}
				if entry.StartsTurn && idx.ListingPreview == "" {
					idx.ListingPreview = truncatePreview(previewProse(UserMessageText(message)))
				}
				idx.MessageCount++
				idx.TranscriptSize += int64(len(line))
			}
			if errors.Is(readErr, io.EOF) {
				done = true
				break
			}
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("build checkpoint display index: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	idx.ContentDigest = digestString(digest)
	identity, known, err := SessionContentIdentity(source)
	if err != nil {
		return err
	}
	if known {
		if identity.DigestHex != idx.ContentDigest {
			return errors.New("checkpoint does not match authoritative identity")
		}
		idx.Revision, idx.RevisionKnown = identity.Revision, identity.RevisionKnown
	}
	body, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO metadata VALUES('source',?),('header',?)`, fingerprint, string(body))
	return err
}
