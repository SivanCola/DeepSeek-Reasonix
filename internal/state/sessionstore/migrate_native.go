package sessionstore

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"reasonix/internal/state/store"
)

// flatImportOutcome is what a flat-import re-home did with one leftover.
type flatImportOutcome int

const (
	// flatImportNotApplicable: nothing matched, or a native move did not land;
	// the caller imports the legacy session the ordinary way.
	flatImportNotApplicable flatImportOutcome = iota
	// flatImportFallback: not a native transcript; copy it and retire the source.
	flatImportFallback
	// flatImportMoved: relocated, old path retired.
	flatImportMoved
	// flatImportDeferred: a runtime still owns the old path. Nothing was copied,
	// moved, or removed; the caller must not import a substitute under the same
	// name, and the one-time marker must not claim the source. A later run can
	// settle it once the holder is gone.
	flatImportDeferred
	// flatImportBlocked: a native transcript was found, but the move could not
	// be completed this run; never hand it to the copy fallback.
	flatImportBlocked
)

// sessionLeaseBlocksMove is the liveness gate renameOverlongSession uses: a
// runtime keeps its session lease while the session is open, so the lease, not
// an mtime coincidence, says whether the path is still owned. Both halves are
// needed: the other-runtime probe is false for our own leases, and past the
// lease-sidecar limit no process can hold one anyway.
func sessionLeaseBlocksMove(path string) bool {
	if sessionLeaseHeldLocally(path) {
		return true
	}
	return sessionLeaseSidecarFits(path) && SessionLeaseHeldByOtherRuntime(path)
}

// moveFlatImport re-homes a session left in the global directory by a flat
// import. An mtime match distinguishes it from a same-named native session.
func moveFlatImport(oldPath, newPath string, srcInfo os.FileInfo) flatImportOutcome {
	if srcInfo == nil {
		return flatImportNotApplicable
	}
	info, err := os.Stat(oldPath)
	if err != nil {
		return flatImportNotApplicable
	}
	d := info.ModTime().Sub(srcInfo.ModTime())
	if d < -2*time.Second || d > 2*time.Second {
		return flatImportNotApplicable
	}
	// The global directory is a live session root: the CLI and the desktop's
	// global tab open sessions there for a runtime's lifetime.
	if sessionLeaseBlocksMove(oldPath) {
		return flatImportDeferred
	}
	switch outcome := moveNativeFlatImport(oldPath, newPath); outcome {
	case flatImportMoved, flatImportDeferred:
		return outcome
	case flatImportBlocked:
		return flatImportNotApplicable
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return flatImportNotApplicable
	}
	if err := linkFileNoReplace(oldPath, newPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return flatImportNotApplicable
		}
		if err := transformAndCopyJsonl(oldPath, newPath); err != nil {
			return flatImportNotApplicable
		}
	}
	// Opening a session takes its lease, so re-prove ownership right before the
	// delete; drop our copy and leave the original for a later run if it moved.
	if sessionLeaseBlocksMove(oldPath) {
		_ = os.Remove(newPath)
		return flatImportDeferred
	}
	if err := os.Remove(oldPath); err != nil {
		return flatImportNotApplicable
	}
	return flatImportMoved
}

// A native flat import must be re-homed from its WAL-backed view, not its
// possibly older compatibility checkpoint.
func moveNativeFlatImport(oldPath, newPath string) flatImportOutcome {
	unlockOld := lockSessionSavePath(oldPath)
	defer unlockOld()
	// The same liveness gate, under the save lock and before the first byte is
	// written to newPath or read from the WAL.
	if sessionLeaseBlocksMove(oldPath) {
		return flatImportDeferred
	}
	probe, err := probeSessionEventLog(oldPath)
	if err != nil || !probe.native || probe.size == 0 {
		return flatImportFallback
	}
	oldFileUnlock, err := lockSessionFile(oldPath)
	if err != nil {
		return flatImportBlocked
	}
	defer oldFileUnlock()

	unlockNew := lockSessionSavePath(newPath)
	defer unlockNew()
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return flatImportBlocked
	}
	newFileUnlock, err := lockSessionFile(newPath)
	if err != nil {
		return flatImportBlocked
	}
	defer newFileUnlock()
	if sessionArtifactExists(newPath) {
		return flatImportBlocked
	}

	session, err := loadSessionUnlocked(oldPath)
	if err != nil {
		return flatImportBlocked
	}
	if err := session.saveLocked(newPath, sessionSaveSnapshot); err != nil {
		return flatImportBlocked
	}
	if meta, ok, metaErr := LoadBranchMeta(oldPath); metaErr != nil {
		return flatImportBlocked
	} else if ok {
		meta.ID = BranchID(newPath)
		if err := SaveBranchMeta(newPath, meta); err != nil {
			return flatImportBlocked
		}
	}
	for _, artifact := range store.SessionSidecarFiles(oldPath) {
		if err := os.Remove(artifact); err != nil && !os.IsNotExist(err) {
			return flatImportBlocked
		}
	}
	if err := os.RemoveAll(store.SessionBlobsDir(oldPath)); err != nil {
		return flatImportBlocked
	}
	if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
		return flatImportBlocked
	}
	return flatImportMoved
}
