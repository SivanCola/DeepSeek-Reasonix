package main

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sort"
	"time"

	"github.com/fsnotify/fsnotify"
	"reasonix/internal/history"
	"reasonix/internal/sessioncatalog"
	"reasonix/internal/store"
)

// Directory notifications admit only dirty roots. The periodic audit remains
// authoritative after dropped notifications and on unsupported filesystems.
func (a *App) watchSessionCatalog(ctx context.Context, catalog *sessioncatalog.Catalog, metadataRequests <-chan struct{}, onAdmitted func()) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	watcher, _ := fsnotify.NewWatcher()
	var events <-chan fsnotify.Event
	var failures <-chan error
	if watcher != nil {
		defer watcher.Close()
		events, failures = watcher.Events, watcher.Errors
	}
	targets := map[string]sessioncatalog.DirectoryTarget{}
	watched := map[string]bool{}
	dirty := map[string]bool{}
	var batch *time.Timer
	var batchReady <-chan time.Time
	armBatch := func() {
		if len(dirty) > 0 && batchReady == nil {
			batch = time.NewTimer(250 * time.Millisecond)
			batchReady = batch.C
		}
	}
	defer func() {
		if batch != nil {
			batch.Stop()
		}
	}()
	// A slow registry projection must not stop receiving filesystem events.
	// One worker coalesces refreshes and is joined before this owner returns.
	refreshMetadata, metadataDone := startCatalogMetadataRefresh(ctx, func(ctx context.Context) {
		if err := a.syncSessionCatalogMetadataBounded(ctx, catalog); err != nil && !errors.Is(err, context.Canceled) {
			slog.Debug("desktop: refresh session catalog metadata", "err", err)
		}
	})
	defer func() { cancel(); <-metadataDone }()
	refreshTargets := func() {
		targets = refreshCatalogWatchTargets(watcher, targets, a.sessionCatalogTargets(), watched, dirty)
	}
	refreshTargets()
	restored := a.tabsRestoredSignal()
	admitted := false
	metadata := time.NewTicker(30 * time.Second)
	audit := time.NewTicker(5 * time.Minute)
	defer metadata.Stop()
	defer audit.Stop()
	maintenance := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-metadataRequests:
			if admitted {
				refreshMetadata()
			}
		case <-restored:
			restored = nil
			// Restored identities can reveal additional roots. Register those
			// watches before allowing their queued discovery to run.
			refreshTargets()
			history.RegisterCatalogRoots(historyCatalogRoots(a.sessionCatalogTargets()))
			a.indexRestoredSessionPaths(ctx, catalog)
			catalog.ResumeDiscovery()
			admitted = true
			onAdmitted()
			refreshMetadata()
			a.requestHistoricalCatalog()
			armBatch()
		case event, ok := <-events:
			if !ok {
				events = nil
				clear(watched)
				continue
			}
			key := filepath.Dir(event.Name)
			// A transcript/sidecar write invalidates one session, not its root.
			if target, exists := targets[key]; exists {
				if path := catalogSessionPathForEvent(event.Name); path != "" && event.Op&(fsnotify.Remove|fsnotify.Rename) == 0 {
					catalog.RequestIndexSession(target, path)
					continue
				}
			}
			if _, exists := targets[filepath.Clean(event.Name)]; exists {
				key = filepath.Clean(event.Name)
				if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
					delete(watched, key)
				}
			}
			if _, exists := targets[key]; exists {
				dirty[key] = true
			}
			armBatch()
		case _, ok := <-failures:
			if !ok {
				failures = nil
			}
			for key := range targets {
				dirty[key] = true
			}
			armBatch()
		case <-metadata.C:
			if admitted {
				refreshMetadata()
			}
			refreshTargets()
			armBatch()
		case <-audit.C:
			refreshTargets()
			keys := make([]string, 0, len(targets))
			for key := range targets {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			if len(keys) > 0 {
				dirty[keys[maintenance%len(keys)]] = true
				maintenance++
			}
			armBatch()
		case <-batchReady:
			batchReady = nil
			for key := range dirty {
				delete(dirty, key)
				target, exists := targets[key]
				if !exists || ctx.Err() != nil {
					continue
				}
				catalog.RequestReconcile(target)
			}
		}
	}
}

func catalogSessionPathForEvent(path string) string {
	if store.IsSessionTranscriptName(filepath.Base(path)) {
		return path
	}
	if filepath.Ext(path) == ".meta" {
		candidate := path[:len(path)-len(".meta")]
		if store.IsSessionTranscriptName(filepath.Base(candidate)) {
			return candidate
		}
	}
	return ""
}

func refreshCatalogWatchTargets(watcher *fsnotify.Watcher, current map[string]sessioncatalog.DirectoryTarget, targets []sessioncatalog.DirectoryTarget, watched, dirty map[string]bool) map[string]sessioncatalog.DirectoryTarget {
	next := map[string]sessioncatalog.DirectoryTarget{}
	for _, target := range targets {
		key := filepath.Clean(target.Path)
		next[key] = target
		if _, known := current[key]; !known {
			dirty[key] = true
		}
		if watcher != nil && !watched[key] {
			watched[key] = watcher.Add(key) == nil
		}
		if !watched[key] {
			dirty[key] = true
		}
	}
	for key := range current {
		if _, exists := next[key]; !exists {
			if watcher != nil && watched[key] {
				_ = watcher.Remove(key)
			}
			delete(watched, key)
			delete(dirty, key)
		}
	}
	return next
}
