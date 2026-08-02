package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

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
type SessionClone struct {
	Path       string
	ownedPaths []string
}

// Discard removes only the files atomically claimed by this clone. Callers
// keep the handle until the new session binding is committed; they never have
// to reconstruct sidecar names and risk deleting somebody else's files.
func (c *SessionClone) Discard() {
	if c == nil {
		return
	}
	removeSessionCloneFiles(c.ownedPaths...)
}

func CloneSessionToPath(srcPath, dstPath string) (*SessionClone, error) {
	if srcPath == "" || dstPath == "" {
		return nil, fmt.Errorf("clone session: source and destination paths are required")
	}
	if canonicalSessionSavePath(srcPath) == canonicalSessionSavePath(dstPath) {
		return nil, fmt.Errorf("clone session: destination must differ from the source")
	}
	// 1. Load the authoritative transcript under the source save-path mutex
	// AND the cross-process file lock — another Reasonix process may be
	// saving the same session right now, and its newest event-log append must
	// not be missed. The lock order matches session.save.
	unlock := lockSessionSavePath(srcPath)
	if cloneLockWaitHook != nil {
		// Signal before blocking on the cross-process lock: tests use this as
		// a deterministic barrier to know the clone is about to wait.
		cloneLockWaitHook()
	}
	unlockFile, err := lockSessionFile(srcPath)
	if err != nil {
		unlock()
		return nil, fmt.Errorf("clone session: lock source file: %w", err)
	}
	session, loadErr := loadSessionUnlocked(srcPath)
	sourceMeta, sourceMetaOK, metaErr := LoadBranchMeta(srcPath)
	unlockFile()
	unlock()
	if loadErr != nil {
		return nil, fmt.Errorf("clone session: load source: %w", loadErr)
	}
	if metaErr != nil {
		return nil, fmt.Errorf("clone session: load source metadata: %w", metaErr)
	}
	// 2. Reserve every destination path (create-only) before Save can replace
	// any of them. Reserving only the checkpoint/log is insufficient because
	// Save atomically replaces the derived event index and branch metadata.
	// A pre-existing sidecar must make the entire clone fail closed.
	// The event log is reserved empty: force saves are checkpoint-only, so the
	// clone needs the native log anchor in place for its own transcript to
	// evolve authoritatively.
	//
	// Only files THIS transaction actually created are ever removed: a
	// pre-existing sidecar (an authoritative event log whose checkpoint never
	// landed, or user metadata) is refused with its bytes untouched.
	clone := &SessionClone{Path: dstPath}
	reserve := func(path string) error {
		if err := reserveSessionClonePath(path); err != nil {
			return err
		}
		clone.ownedPaths = append(clone.ownedPaths, path)
		return nil
	}
	for _, path := range []string{
		dstPath,
		store.SessionEventLog(dstPath),
		store.SessionEventIndex(dstPath),
	} {
		if err := reserve(path); err != nil {
			clone.Discard()
			return nil, err
		}
	}
	// Session.Save reads the branch-meta CAS ledger before recording a content
	// revision, so the create-only metadata reservation must already contain a
	// valid fresh record rather than an empty placeholder.
	if err := reserveSessionCloneMeta(dstPath, sourceMeta, sourceMetaOK); err != nil {
		clone.Discard()
		return nil, err
	}
	clone.ownedPaths = append(clone.ownedPaths, store.SessionMeta(dstPath))
	// 3. Save the complete session; the empty reserved checkpoint receives the
	// full transcript.
	if err := session.Save(dstPath); err != nil {
		clone.Discard()
		return nil, fmt.Errorf("clone session: save destination: %w", err)
	}
	return clone, nil
}

func removeSessionCloneFiles(paths ...string) {
	for _, path := range paths {
		if path != "" {
			_ = os.Remove(path)
		}
	}
}

func reserveSessionCloneMeta(sessionPath string, source BranchMeta, sourceOK bool) error {
	path := store.SessionMeta(sessionPath)
	when := time.Now().UTC()
	meta := BranchMeta{
		ID:        BranchID(sessionPath),
		CreatedAt: when,
		UpdatedAt: when,
	}
	if sourceOK {
		// Keep the desktop binding and user-selected runtime profile so opening a
		// copy stays in the same workspace/topic. Lineage, recovery, in-flight,
		// and persistence-ledger fields intentionally start fresh: the copy is an
		// independent session whose first Save owns its own revision history.
		meta.Name = source.Name
		meta.Scope = source.Scope
		meta.WorkspaceRoot = source.WorkspaceRoot
		meta.TopicID = source.TopicID
		meta.TopicTitle = source.TopicTitle
		meta.CustomTitle = source.CustomTitle
		meta.Model = source.Model
		meta.TokenMode = source.TokenMode
		meta.Mode = source.Mode
		meta.ToolApprovalMode = source.ToolApprovalMode
		meta.Goal = source.Goal
		meta.SchemaVersion = source.SchemaVersion
		meta.Turns = source.Turns
		meta.Preview = source.Preview
	}
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("clone session: encode branch metadata: %w", err)
	}
	b = append(b, '\n')
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("clone session: reserve destination: %w", err)
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("clone session: write branch metadata reservation: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("clone session: close branch metadata reservation: %w", err)
	}
	return nil
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

// cloneLockWaitHook, when set, is invoked immediately before the clone waits
// for the source's cross-process file lock. Tests use it as a deterministic
// barrier, removing timing-based false positives.
var cloneLockWaitHook func()
