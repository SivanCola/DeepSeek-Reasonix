package agent

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/fileops"
	"reasonix/internal/historywork"
	"reasonix/internal/projectiondb"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

func TestCheckpointPagerResumesDurableBatchAfterReopen(t *testing.T) {
	for _, mode := range []string{"resume", "changed-source", "damaged-progress", "missing-progress"} {
		t.Run(mode, func(t *testing.T) { checkCheckpointPagerResume(t, mode) })
	}
}

func TestCheckpointPagerRejectsSourceChangeBeforePublication(t *testing.T) {
	for _, mode := range []string{"rewrite", "replace", "event-log"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "history.jsonl")
			body := []byte("{\"role\":\"user\",\"content\":\"original\"}\n")
			if err := os.WriteFile(source, body, 0600); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(source)
			if err != nil {
				t.Fatal(err)
			}
			target, version := fileops.DiskSnapshot(source, info)
			fingerprint := fmt.Sprintf("%s:%s:checkpoint", target.Key, version)
			opts := projectiondb.OpenOptions{Path: filepath.Join(dir, "display.sqlite"), Migrations: displayPagerMigrations, RequireDisk: true, ResumeKey: fingerprint}
			err = projectiondb.Rebuild(t.Context(), opts, func(ctx context.Context, db *sql.DB) error {
				return buildCheckpointDisplayPagerObserved(ctx, db, source, fingerprint, func(int) {
					switch mode {
					case "replace":
						if err := os.Rename(source, source+".previous"); err != nil {
							t.Fatal(err)
						}
					case "event-log":
						if err := os.WriteFile(store.SessionEventLog(source), []byte("{}\n"), 0600); err != nil {
							t.Fatal(err)
						}
						return
					}
					if err := os.WriteFile(source, bytes.ReplaceAll(body, []byte("original"), []byte("replaced")), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Chtimes(source, info.ModTime(), info.ModTime()); err != nil {
						t.Fatal(err)
					}
				})
			})
			if !errors.Is(err, ErrDisplaySourceChanged) && !(mode == "event-log" && errors.Is(err, ErrDisplayFormatUnsupported)) {
				t.Fatalf("source change was published: %v", err)
			}
			if _, err := os.Stat(opts.Path); !os.IsNotExist(err) {
				t.Fatalf("invalid index published: %v", err)
			}
		})
	}
}

func checkCheckpointPagerResume(t *testing.T, mode string) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "history.jsonl")
	cache := filepath.Join(t.TempDir(), "display.sqlite")
	var body bytes.Buffer
	messages := make([]provider.Message, 2*historywork.BatchEntries)
	for i := range messages {
		messages[i] = provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("message-%d %s", i, strings.Repeat("a", 4096))}
		if err := json.NewEncoder(&body).Encode(messages[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(source, body.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	target, version := fileops.DiskSnapshot(source, info)
	fingerprint := fmt.Sprintf("%s:%s:checkpoint", target.Key, version)
	opts := projectiondb.OpenOptions{Path: cache, Migrations: displayPagerMigrations, RequireDisk: true, MaxOpenConns: 1, ResumeKey: "checkpoint-v1:" + fingerprint}
	ctx, cancel := context.WithCancel(t.Context())
	err = projectiondb.Rebuild(ctx, opts, func(ctx context.Context, db *sql.DB) error {
		return buildCheckpointDisplayPagerObserved(ctx, db, source, fingerprint, func(count int) {
			if count != historywork.BatchEntries {
				t.Fatalf("checkpoint was not a complete batch: %d", count)
			}
			cancel()
		})
	})
	cancel()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected retained interrupted preparation: %v", err)
	}
	switch mode {
	case "changed-source":
		body.Reset()
		for i := range messages {
			messages[i].Content = strings.ReplaceAll(messages[i].Content, "a", "b")
			if err := json.NewEncoder(&body).Encode(messages[i]); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(source, body.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(source, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
	case "damaged-progress", "missing-progress":
		pending := opts
		pending.Path += ".rebuild-pending"
		handle, err := projectiondb.Open(t.Context(), pending)
		if err != nil {
			t.Fatal(err)
		}
		query := `UPDATE metadata SET value='broken' WHERE key='checkpoint_progress'`
		if mode == "missing-progress" {
			query = `DELETE FROM metadata WHERE key='checkpoint_progress'`
		}
		_, err = handle.DB.Exec(query)
		_ = handle.DB.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	// A new Open must read only the suffix, restore the semantic digest state,
	// and publish the same complete view as uninterrupted native replay.
	meter := &historywork.Coordinator{}
	pager, err := OpenDisplayPager(meter.Context(t.Context()), source, cache)
	if err != nil {
		t.Fatal(err)
	}
	defer pager.Close()
	if got := meter.Diagnostics().InstrumentedReadBytes; mode == "resume" && got >= int64(body.Len())*3/4 {
		t.Fatalf("reopen reread the completed prefix: %d of %d bytes", got, body.Len())
	}
	digest, err := ContentDigestForMessages(messages)
	if err != nil || pager.Header.ContentDigest != digest || pager.Header.MessageCount != len(messages) {
		t.Fatalf("resumed semantic view differs: count=%d digest=%s err=%v", pager.Header.MessageCount, pager.Header.ContentDigest, err)
	}
	last, err := pager.Entry(len(messages) - 1)
	if err != nil || last.AuthoredTurn != len(messages) {
		t.Fatalf("turn state was not restored: %+v %v", last, err)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(after, body.Bytes()) {
		t.Fatalf("preparation changed the source: %v", err)
	}
}
