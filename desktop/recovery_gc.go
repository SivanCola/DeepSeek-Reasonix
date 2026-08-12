package main

import (
	"log/slog"
	"os"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/sessioncatalog"
)

// recoveryGCInterval bounds how often the background sweep repeats after the
// startup run. Covered copies are also reclaimed by the shorter startup grace.
const recoveryGCInterval = 6 * time.Hour

// recoveryGCFollowUpDelay re-runs the short-grace sweep once after startup so
// branches that just crossed the 15-minute idle line are not left until the
// next 6-hour tick.
const recoveryGCFollowUpDelay = 20 * time.Minute

// startRecoveryGC is intentionally a no-op for physical moves.
//
// Catalog v4 folds recovery lineages into one ordinary list row. Automatic
// startup/upgrade/timer GC must not move JSONL/meta into trash — only explicit
// CleanRecoveryLineage (UI, with preview) and `reasonix sessions cleanup
// --apply` may reclaim covered copies. reclaimRecoveryBranchesIn remains for
// those explicit entry points and focused tests.
func (a *App) startRecoveryGC() {}

func waitRecoveryGCStartup(done <-chan struct{}, elapsed <-chan time.Time) bool {
	select {
	case <-elapsed:
		return true
	case <-done:
		return false
	}
}

// sweepReclaimableRecoveryBranches trashes conflict-recovery branches that
// preserve nothing unique (their content is covered by a still-present parent
// session), were never continued on, sat idle past the grace period, and are
// not held by any runtime. Trashing — never hard deletion — keeps every swept
// branch recoverable from the session trash. Returns how many were reclaimed.
func (a *App) sweepReclaimableRecoveryBranches() int {
	return a.sweepReclaimableRecoveryBranchesWithGrace(agent.RecoveryGCGracePeriod)
}

func (a *App) sweepReclaimableRecoveryBranchesWithGrace(grace time.Duration) int {
	return a.reclaimRecoveryBranchesIn(recoveryGCDirs(), time.Now(), grace)
}

func (a *App) reclaimRecoveryBranchesIn(dirs []string, now time.Time, grace time.Duration) int {
	if grace <= 0 {
		grace = agent.RecoveryGCGracePeriod
	}
	reclaimed := 0
	for _, dir := range dirs {
		removed := map[string]bool{}
		if catalog := a.sessionCatalog.Load(); catalog != nil {
			groups, err := catalog.ListRecoveryGroups(a.bootContext(), dir)
			if err != nil {
				slog.Warn("desktop: list recovery lineages for GC", "dir", dir, "err", err)
			} else {
				for _, group := range groups {
					for _, path := range a.reclaimAdoptedRecoveryGroup(group, now, grace) {
						removed[sessionRuntimeKey(path)] = true
						reclaimed++
					}
				}
			}
		}
		reclaimable, err := agent.ReclaimableRecoveryBranches(dir, now, grace)
		if err != nil {
			slog.Warn("desktop: scan reclaimable recovery branches", "dir", dir, "err", err)
			continue
		}
		for _, path := range reclaimable {
			if removed[sessionRuntimeKey(path)] {
				continue
			}
			// Re-check liveness right before disposal: the scan is a snapshot,
			// and the user may have opened the branch since.
			if agent.SessionLeaseHeld(path) || a.sessionOpenInAnyTab(path) {
				continue
			}
			// DeleteRecoveryCopy re-proves real parent coverage under removal
			// guards. A concurrent continue-edit, missing parent, or busy lease
			// skips without moving or permanently deleting anything.
			if err := a.DeleteRecoveryCopy(path); err != nil {
				slog.Warn("desktop: trash reclaimed recovery branch", "path", path, "err", err)
				continue
			}
			reclaimed++
		}
	}
	if reclaimed > 0 {
		slog.Info("desktop: moved redundant recovery branches to the session trash",
			"count", reclaimed, "grace", grace.String())
	}
	return reclaimed
}

// reclaimAdoptedRecoveryGroup compacts an entire legacy recovery chain once a
// canonical leaf has proved it covers every member. Every candidate is still
// revalidated under removal guards immediately before it is moved to trash.
func (a *App) reclaimAdoptedRecoveryGroup(group sessioncatalog.RecoveryGroup, now time.Time, grace time.Duration) []string {
	if group.State != "adopted" || group.CanonicalPath == "" || group.ID == "" {
		return nil
	}
	candidates := []string{}
	for _, member := range group.Members {
		if member.Path == group.CanonicalPath || member.RecoveryRole != sessioncatalog.RecoveryRoleCoveredCopy {
			continue
		}
		info, err := os.Stat(member.Path)
		if err != nil || now.Sub(info.ModTime()) < grace {
			continue
		}
		candidates = append(candidates, member.Path)
	}
	if len(candidates) == 0 {
		return nil
	}
	defer a.lockRuntimeMutation("gc-recovery-lineage")()
	a.sessionRemovalMu.Lock()
	defer a.sessionRemovalMu.Unlock()
	if a.sessionOpenInAnyTab(group.CanonicalPath) || agent.SessionLeaseHeld(group.CanonicalPath) {
		return nil
	}
	if err := agent.ReparentRecoveryCanonical(group.CanonicalPath, group.ID, group.Directory); err != nil {
		return nil
	}
	moved := []string{}
	for _, path := range candidates {
		if a.sessionOpenInAnyTab(path) || agent.SessionLeaseHeld(path) {
			continue
		}
		if err := agent.TrashRecoveryBranchCoveredBy(path, group.CanonicalPath, group.Directory); err != nil {
			continue
		}
		moved = append(moved, path)
		a.removeSessionCatalogPath(path, "recovery_lineage_gc")
	}
	return moved
}

// recoveryGCDirs returns every session directory the desktop lists sessions
// from: the global desktop and legacy shared dirs plus each saved project's
// session dirs, deduplicated.
func recoveryGCDirs() []string {
	seen := map[string]bool{}
	var dirs []string
	add := func(dir string) {
		key := projectRootKey(dir)
		if dir == "" || seen[key] {
			return
		}
		seen[key] = true
		dirs = append(dirs, dir)
	}
	add(desktopSessionDir(globalWorkspaceRoot()))
	add(config.SessionDir())
	for _, project := range loadProjectsFile().Projects {
		if root := normalizeProjectRoot(project.Root); root != "" {
			add(desktopSessionDir(root))
			add(config.ProjectSessionDir(root))
		}
	}
	return dirs
}

// sessionOpenInAnyTab reports whether any tab's current session is path.
// Lease checks cover live runtimes; this additionally covers tabs that hold a
// session without a lease (read-only channel views).
func (a *App) sessionOpenInAnyTab(path string) bool {
	key := sessionRuntimeKey(path)
	if key == "" {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, tab := range a.tabs {
		if tab == nil {
			continue
		}
		if sessionRuntimeKey(tab.currentSessionPath()) == key {
			return true
		}
	}
	return false
}
