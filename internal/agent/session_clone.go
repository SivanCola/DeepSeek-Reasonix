package agent

import (
	"fmt"
	"os"

	"reasonix/internal/store"
)

// CloneSessionToPath clones the authoritative transcript of the session at
// srcPath into a brand-new session file at dstPath.
//
// The .jsonl checkpoint is only a compatibility anchor: once the adjacent
// event log exists, it is the authoritative transcript and may hold turns the
// checkpoint has not caught up to. The clone therefore acquires the source
// save-path mutex AND the cross-process session file lock (the same order a
// save uses), replays the event log, reserves the destination files with
// O_EXCL, saves the complete session, and creates fresh branch metadata. Any
// failure removes every partial destination sidecar so a stale or truncated
// copy can never be adopted.
func CloneSessionToPath(srcPath, dstPath string) error {
	if srcPath == "" || dstPath == "" {
		return fmt.Errorf("clone session: source and destination paths are required")
	}
	if canonicalSessionSavePath(srcPath) == canonicalSessionSavePath(dstPath) {
		return fmt.Errorf("clone session: destination must differ from the source")
	}
	// 1. Load the authoritative transcript under the source save-path mutex
	// AND the cross-process file lock — another Reasonix process may be
	// saving the same session right now, and its newest event-log append must
	// not be missed. The lock order matches session.save.
	unlock := lockSessionSavePath(srcPath)
	unlockFile, err := lockSessionFile(srcPath)
	if err != nil {
		unlock()
		return fmt.Errorf("clone session: lock source file: %w", err)
	}
	session, loadErr := loadSessionUnlocked(srcPath)
	unlockFile()
	unlock()
	if loadErr != nil {
		return fmt.Errorf("clone session: load source: %w", loadErr)
	}
	// 2. Reserve the destination paths (create-only) so a concurrent writer
	// cannot claim them, and so a failed save never leaves a partial clone.
	// The event log is reserved empty: force saves are checkpoint-only, so the
	// clone needs the native log anchor in place for its own transcript to
	// evolve authoritatively.
	if err := reserveSessionClonePath(dstPath); err != nil {
		return err
	}
	cleanup := func() {
		removeSessionCloneFiles(dstPath)
	}
	if err := reserveSessionClonePath(store.SessionEventLog(dstPath)); err != nil {
		cleanup()
		return err
	}
	// 3. Save the complete session; the empty reserved checkpoint receives the
	// full transcript.
	if err := session.Save(dstPath); err != nil {
		cleanup()
		return fmt.Errorf("clone session: save destination: %w", err)
	}
	// 4. Fresh branch metadata; a failure must not leave an adoptable clone
	// without topic ownership.
	if _, err := EnsureBranchMeta(dstPath); err != nil {
		cleanup()
		return fmt.Errorf("clone session: branch metadata: %w", err)
	}
	return nil
}

// removeSessionCloneFiles removes every file a clone can create at dstPath:
// the checkpoint, the event log, the event index and the branch metadata.
func removeSessionCloneFiles(dstPath string) {
	for _, path := range []string{
		dstPath,
		store.SessionEventLog(dstPath),
		store.SessionEventIndex(dstPath),
		store.SessionMeta(dstPath),
	} {
		if path != "" {
			_ = os.Remove(path)
		}
	}
}

// reserveSessionClonePath creates dstPath with O_EXCL semantics so the clone
// never overwrites an existing session file.
func reserveSessionClonePath(dstPath string) error {
	f, err := os.OpenFile(dstPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("clone session: reserve destination: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(dstPath)
		return fmt.Errorf("clone session: close reserved destination: %w", err)
	}
	return nil
}
