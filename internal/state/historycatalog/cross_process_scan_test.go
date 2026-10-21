//go:build !windows

package historycatalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"reasonix/internal/base/backgroundwork"
	"reasonix/internal/base/testenv"
	"reasonix/internal/contract/provider"
	"reasonix/internal/state/sessionstore"
)

// Two Catalog values opened over one temp sqlite path model two Reasonix
// processes that share a cache directory (and therefore one v1.sqlite). The
// model is faithful where the shared state lives: both catalogs use separate
// sql.DB pools against the same WAL file, so every write below is a real
// cross-connection write serialized by SQLite, and neither catalog can see the
func openSharedCatalog(t *testing.T, path string) *Catalog {
	t.Helper()
	catalog, err := Open(context.Background(), Options{Path: path, ReconcileInterval: time.Hour, Work: backgroundwork.New(backgroundwork.Options{})})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	return catalog
}

func waitForRootScanning(t *testing.T, catalog *Catalog, root string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var state string
		err := catalog.db.QueryRow(`SELECT state FROM history_roots WHERE path=?`, root).Scan(&state)
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the scan never recorded its history_roots row (last error %v)", err)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// A scan decides a source is missing from one fact: the source's
// seen_generation is not the scan's generation. That fact is only true of rows
// that existed when the scan listed the directory. Another process can index a
// conversation after the listing (a persist event indexes with generation 0),
// and this scan's final sweep then marks that live, fully indexed, freshly
func TestScanDoesNotHideSourceAnotherProcessIndexedDuringIt(t *testing.T) {
	ctx := context.Background()
	root := testenv.TempDir(t)
	dbPath := filepath.Join(testenv.TempDir(t), "history.sqlite")

	// A transcript name with no sidecars: loading it reads the .jsonl bytes, so
	// an empty pipe open blocks the scan exactly where a slow decode would.
	parked := filepath.Join(root, "aa-parked.jsonl")
	if err := syscall.Mkfifo(parked, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			// The blocked reader is already in open(2); this handshake is what
			// makes the release deterministic rather than timed.
			writer, err := os.OpenFile(parked, os.O_WRONLY, 0)
			if err != nil {
				return
			}
			payload, _ := json.Marshal(provider.Message{Role: provider.RoleUser, Content: "parked transcript"})
			_, _ = writer.Write(append(payload, '\n'))
			_ = writer.Close()
		})
	}
	t.Cleanup(release)

	scanning := openSharedCatalog(t, dbPath) // process A: the directory scan
	writing := openSharedCatalog(t, dbPath)  // process B: a save's persist event
	target := Root{Path: root, Source: "project", Scope: "project", WorkspaceRoot: root}

	scanDone := make(chan error, 1)
	go func() { scanDone <- scanning.ReconcileRoot(ctx, target) }()

	// The listing is complete once the root row exists: reconcileRoot writes it
	// after ReadDir and before it opens any transcript.
	waitForRootScanning(t, writing, root)

	// Process B saves a new conversation and indexes it from the persist event,
	// which is what the other frontend does while this scan runs. A fresh
	// session's snapshot event carries AppendFrom -1.
	fresh := filepath.Join(root, "zz-fresh.jsonl")
	saveMessages(t, fresh, provider.Message{Role: provider.RoleUser, Content: "zephyr freshly saved decision"})
	if !writing.EnqueuePersist(target, sessionstore.SessionPersistEvent{Path: fresh, AppendFrom: -1, MessageCount: 1}) {
		t.Fatal("the persist event was not queued")
	}
	if err := writing.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if docs := sourceDocumentCount(t, writing, fresh); docs == 0 {
		t.Fatal("fixture: the persist event did not index the new conversation")
	}

	release()
	select {
	case err := <-scanDone:
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the parked scan never finished")
	}

	// The file exists, its rows are indexed, and a live process measured it
	// healthy during the scan. The scan that never saw it must not overwrite
	// that measurement with "missing".
	if health := sourceHealthOf(t, writing, fresh); health != "ok" {
		t.Fatalf("a conversation indexed during the scan is health=%q; the scan marked a live source missing from a stale listing", health)
	}
	found, err := writing.Search(ctx, SearchRequest{Query: "zephyr freshly saved", Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Items) != 1 {
		t.Fatalf("search after the scan returned %d item(s), want the conversation the other process indexed", len(found.Items))
	}
}

// Control: the sweep that now measures absence still retires a transcript that
// really is gone. Its rows stay searchable through the grace window and are
// removed only after a later scan confirms the file is still absent.
