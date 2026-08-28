package main

import (
	"errors"
	"time"

	"reasonix/internal/sessioncatalog"
)

var errSessionCatalogStopTimeout = errors.New("session catalog did not stop before the rebuild deadline")

// RebuildSessionCatalog owns the bounded, one-shot rebuild transaction. The
// replacement watcher is always an ordinary watcher and never owns rebuilding.
func (a *App) RebuildSessionCatalog() error {
	return a.rebuildSessionCatalog(5 * time.Second)
}

func (a *App) rebuildSessionCatalog(stopTimeout time.Duration) error {
	if a == nil || a.shuttingDown.Load() {
		return errors.New("application is shutting down")
	}
	if !a.catalogRebuilding.CompareAndSwap(false, true) {
		return nil
	}
	status := a.currentSessionCatalogStatus()
	finishedRevision := status.Revision
	a.emitProjectTreeChangedV2(status.Revision, nil, "catalog_rebuild_started")
	defer func() {
		if !a.shuttingDown.Load() {
			// Arm the ordinary watcher before releasing the single-flight gate so
			// another Wails rebuild cannot enter the stop/start handoff gap.
			a.startSessionCatalog()
		}
		a.catalogRebuilding.Store(false)
		if !a.shuttingDown.Load() {
			a.emitProjectTreeChangedV2(finishedRevision, nil, "catalog_rebuild_finished")
		}
	}()

	// Rebuild must not race the old SQLite handle on Windows, where publishing
	// the atomic replacement can fail while that handle is still closing.
	if !a.stopSessionCatalog(stopTimeout) {
		return errSessionCatalogStopTimeout
	}
	replacement, err := sessioncatalog.RebuildWithRevisionFloor(
		a.bootContext(), sessioncatalog.DefaultPath(), a.sessionCatalogTargets(), status.Revision,
	)
	if err == nil {
		finishedRevision = max(finishedRevision, replacement.Revision)
	}
	return err
}
