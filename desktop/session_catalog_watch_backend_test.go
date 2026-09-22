package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"reasonix/internal/sessioncatalog"
)

type recoveringCatalogWatch struct {
	workspaceWatcher
	fail bool
	adds int
}

func (w *recoveringCatalogWatch) Add(string, bool) error {
	w.adds++
	if w.fail {
		return errors.New("watch temporarily unavailable")
	}
	return nil
}

func TestCatalogWatchRecoveryReconcilesOnlyTheUnobservedInterval(t *testing.T) {
	root := canonicalWorkspaceRoot(t.TempDir())
	targets := []sessioncatalog.DirectoryTarget{{Path: root, Scope: "global"}}
	watched, dirty := map[string]bool{}, map[string]bool{}
	watcher := &recoveringCatalogWatch{fail: true}
	current := refreshCatalogWatchTargets(watcher, nil, targets, watched, dirty)
	if !dirty[root] || watched[root] {
		t.Fatal("failed initial watch omitted initial discovery")
	}
	clear(dirty)
	current = refreshCatalogWatchTargets(watcher, current, targets, watched, dirty)
	if len(dirty) != 0 || watcher.adds != 2 {
		t.Fatal("watch retry must not schedule full discovery on every metadata tick")
	}
	watcher.fail = false
	current = refreshCatalogWatchTargets(watcher, current, targets, watched, dirty)
	if !dirty[root] || !watched[root] {
		t.Fatal("recovered watch failed to reconcile its observation gap")
	}
	clear(dirty)
	refreshCatalogWatchTargets(watcher, current, targets, watched, dirty)
	if len(dirty) != 0 || watcher.adds != 3 {
		t.Fatal("settled watch restarted registration or discovery")
	}
}

func TestCatalogWatchCanonicalEventKeepsRegisteredAccessPath(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(source, []byte("not a valid transcript"), 0600); err != nil {
		t.Fatal(err)
	}
	updated := make(chan struct{}, 1)
	catalog, err := sessioncatalog.Open(t.Context(), sessioncatalog.Options{InMemory: true, MetadataOnly: true, StartPaused: true,
		OnRevision: func(uint64, []string, string) {
			select {
			case updated <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close(context.Background())
	watched, dirty := map[string]bool{}, map[string]bool{}
	targets := refreshCatalogWatchTargets(nil, nil, []sessioncatalog.DirectoryTarget{{Path: dir, Scope: "global"}}, watched, dirty)
	clear(dirty)
	key := canonicalWorkspaceRoot(dir)
	admitCatalogWatchEvent(catalog, fsnotify.Event{Name: filepath.Join(key, "session.jsonl.meta"), Op: fsnotify.Write}, targets, watched, dirty)
	if len(dirty) != 0 {
		t.Fatalf("exact metadata event scheduled a root scan: %v", dirty)
	}
	select {
	case <-updated:
	case <-time.After(sessionCatalogTestDeadline):
		t.Fatal("canonical event failed to update the exact path")
	}
	record, exists, err := catalog.GetSession(t.Context(), source)
	if err != nil || !exists || record.Path != source {
		t.Fatalf("watch event changed access identity: %+v %v %v", record, exists, err)
	}
	// A removed root must drop its watch and schedule reconciliation even
	// when there is no current filesystem object to canonicalize.
	watched[key] = true
	admitCatalogWatchEvent(catalog, fsnotify.Event{Name: key, Op: fsnotify.Remove}, targets, watched, dirty)
	if watched[key] || !dirty[key] {
		t.Fatalf("root removal lost invalidation: watched=%v dirty=%v", watched, dirty)
	}
}
