package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"reasonix/internal/agent"
)

// Session ownership classification. The lease metadata plus the OS lock and
// the process-local runtime registry decide who holds a session, so the UI no
// longer collapses "another window", a background task, an external process
// and a stale lock into one opaque error.
const (
	sessionOwnerCurrentTab      = "current_tab"          // another visible tab in this window
	sessionOwnerCurrentDetached = "current_detached"     // a background task detached from its tab in this process
	sessionOwnerSameHidden      = "same_instance_hidden" // this instance, window hidden to the tray
	sessionOwnerExternal        = "external_process"     // another process holds the OS lease
	sessionOwnerStale           = "stale_reclaimed"      // metadata stale, OS lock released
	sessionOwnerUnknown         = "unknown"              // cannot be determined safely
)

// sessionOwnerActions maps each owner kind to the allowed resolution actions.
func sessionOwnerActions(kind string) []string {
	switch kind {
	case sessionOwnerCurrentTab, sessionOwnerCurrentDetached, sessionOwnerSameHidden:
		return []string{"focus"}
	case sessionOwnerExternal:
		return []string{"retry", "read_only", "copy"}
	case sessionOwnerStale:
		return []string{"retry"}
	default:
		return []string{"read_only", "copy"}
	}
}

// classifySessionOwner determines who holds the session at path. The lease
// error carries the recorded holder metadata; the OS lock probe decides
// whether that holder is live. When heldByDetached is true, the holder is a
// background task detached from its tab instead of a visible tab.
func classifySessionOwner(path string, leaseErr *agent.SessionLeaseError, heldByDetached bool) (kind string, holderPID int, holderHost, acquiredAt, holderSince string) {
	// 1. This process holds the lease: distinguish a detached background task,
	// a visible tab of this window, and a hidden window of this same instance.
	if agent.SessionLeaseHeldByCurrentRuntime(path) {
		switch {
		case heldByDetached:
			return sessionOwnerCurrentDetached, os.Getpid(), hostnameOf(leaseErr), "", ""
		case backgroundWindowHidden.Load():
			return sessionOwnerSameHidden, os.Getpid(), hostnameOf(leaseErr), "", ""
		default:
			return sessionOwnerCurrentTab, os.Getpid(), hostnameOf(leaseErr), "", ""
		}
	}
	// 2. Another process holds the OS lock: external owner.
	if agent.SessionLeaseHeldByOtherRuntime(path) {
		kind = sessionOwnerExternal
		if leaseErr != nil && leaseErr.Info != nil {
			holderPID = leaseErr.Info.PID
			holderHost = strings.TrimSpace(leaseErr.Info.Hostname)
			if !leaseErr.Info.AcquiredAt.IsZero() {
				acquiredAt = leaseErr.Info.AcquiredAt.UTC().Format(time.RFC3339)
				holderSince = time.Since(leaseErr.Info.AcquiredAt).Round(time.Second).String()
			}
		}
		return kind, holderPID, holderHost, acquiredAt, holderSince
	}
	// 3. No live owner: either stale metadata (auto-reclaimable) or the
	// holder already left without cleanup.
	if leaseErr != nil && leaseErr.Info != nil {
		return sessionOwnerStale, leaseErr.Info.PID, strings.TrimSpace(leaseErr.Info.Hostname), "", ""
	}
	return sessionOwnerUnknown, 0, "", "", ""
}

func hostnameOf(leaseErr *agent.SessionLeaseError) string {
	if leaseErr != nil && leaseErr.Info != nil {
		return strings.TrimSpace(leaseErr.Info.Hostname)
	}
	return ""
}

// sessionHeldByDetached reports whether a detached background runtime (a tab
// closed while its controller kept working) holds the session key.
// sessionHeldByDetached reports whether a detached background runtime (a tab
// closed while its controller kept working) holds the session key.
//
// It must only be called while App.mu is already held (every caller is a
// sessionRuntimeViewLocked/setSessionRuntimePhaseLocked path), so it reads
// the map directly instead of re-locking — a nested RLock here would deadlock
// once a writer is waiting.
func (a *App) sessionHeldByDetached(path string) bool {
	if path == "" {
		return false
	}
	key := sessionRuntimeKey(path)
	if key == "" {
		return false
	}
	for _, tab := range a.detachedSessions {
		if tab != nil && sessionRuntimeKey(tab.SessionPath) == key {
			return true
		}
	}
	return false
}

// ResolveSessionRuntimeIssue executes one session-ownership resolution action
// (focus | retry | read_only | copy). The action must be one of the actions
// advertised by the issue, the issue ID must match the tab's current issue,
// the runtime epoch must be unchanged, and the lease ownership is re-checked
// before anything runs — expired actions are rejected.
func (a *App) ResolveSessionRuntimeIssue(tabID, issueID, action string) error {
	return a.resolveSessionIssueActions(tabID, issueID, action)
}

func (a *App) resolveSessionIssueActions(tabID, issueID, action string) error {
	// One locked snapshot: tab binding, session path, runtime view (issue and
	// epoch) and detached ownership are all captured together so the checks
	// below cannot observe torn state.
	a.mu.RLock()
	tab := a.tabs[tabID]
	if tab == nil {
		a.mu.RUnlock()
		return fmt.Errorf("tab %q is no longer open", tabID)
	}
	runtimeView := a.sessionRuntimeViewLocked(tab)
	path := tab.SessionPath
	detached := a.sessionHeldByDetached(path)
	a.mu.RUnlock()
	issue := runtimeView.Issue
	if issue == nil {
		return fmt.Errorf("no session issue is pending for this tab")
	}
	// The issue must be bound to a concrete runtime epoch; synthesized issues
	// (e.g. from a stale StartupErr projection) cannot be resolved.
	if strings.TrimSpace(issue.IssueID) == "" || strings.TrimSpace(runtimeView.Epoch) == "" {
		return fmt.Errorf("session issue is not resolvable")
	}
	if issueID != issue.IssueID {
		return fmt.Errorf("session issue %q is no longer current", issueID)
	}
	// The runtime must not have advanced past the issue's epoch.
	if strings.TrimSpace(issue.epoch) != "" && issue.epoch != runtimeView.Epoch {
		return fmt.Errorf("session runtime advanced since the issue was raised")
	}
	// The action must be one this issue advertises.
	allowed := false
	for _, candidate := range issue.Actions {
		if candidate == action {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("session issue action %q is not allowed for %s", action, issue.OwnerKind)
	}
	// Re-verify the lease ownership before acting: the classification may have
	// changed (external holder released the lease, stale metadata reclaimed).
	// classifySessionOwner probes the OS lease itself and needs no App lock;
	// the detached snapshot was captured above under the lock.
	kind, _, _, _, _ := classifySessionOwner(path, nil, detached)
	if kind == sessionOwnerExternal && issue.OwnerKind != sessionOwnerExternal {
		return fmt.Errorf("session lease ownership changed since the issue was raised")
	}
	if issue.OwnerKind == sessionOwnerExternal && kind != sessionOwnerExternal && kind != sessionOwnerStale && kind != sessionOwnerUnknown {
		return fmt.Errorf("external session holder released the lease; retry instead")
	}
	switch action {
	case "focus":
		return a.resolveSessionIssueFocus(tab, issue.OwnerKind)
	case "retry":
		return a.commitSessionIssueAction(tab, action, path, runtimeView.Epoch)
	case "read_only":
		return a.commitSessionIssueAction(tab, action, path, runtimeView.Epoch)
	case "copy":
		return a.commitSessionIssueAction(tab, action, path, runtimeView.Epoch)
	default:
		return fmt.Errorf("session issue action %q is not allowed", action)
	}
}

// commitSessionIssueAction performs the retry/read_only/copy action. The
// binding comparison and the minimal state mutation happen in ONE write-
// locked critical section, so a concurrent rebind cannot pass the comparison
// and then receive a stale action; the rebuild is scheduled after the lock is
// released. The copy path additionally discards the new clone when the
// binding changed before commit.
func (a *App) commitSessionIssueAction(tab *WorkspaceTab, action, boundPath, boundEpoch string) error {
	switch action {
	case "copy":
		return a.reopenSessionCopy(tab, boundPath, boundEpoch)
	}
	// retry / read_only: compare and mutate atomically under the write lock.
	a.mu.Lock()
	if tab.SessionPath != boundPath || a.sessionRuntimeViewLocked(tab).Epoch != boundEpoch {
		a.mu.Unlock()
		return fmt.Errorf("session state advanced since the action was requested")
	}
	if action == "read_only" {
		tab.ReadOnly = true
	}
	clearTabStartupError(tab)
	a.mu.Unlock()
	a.goSafe("sessionIssueAction:"+action, func() {
		a.buildTabControllerWithContext(tab, loadedTabSession{}, a.bootContext(), 0, nil)
	})
	return nil
}

func (a *App) resolveSessionIssueFocus(tab *WorkspaceTab, ownerKind string) error {
	// The window may be hidden (tray): restore it first.
	if backgroundWindowHidden.Load() {
		a.showMainWindowFrom("session_issue_focus")
	}
	if ownerKind == sessionOwnerCurrentTab {
		// Focus the tab that actually holds the session: find the owner tab
		// through the runtime registry and activate it. The lease runtime key
		// mirror is lock-free (sessionLease itself is protected by
		// sessionLeaseMu and must not be read under App.mu).
		targetKey := sessionRuntimeKey(tab.SessionPath)
		a.mu.RLock()
		for _, candidate := range a.tabs {
			if candidate == nil || candidate == tab {
				continue
			}
			if candidate.Ctrl != nil && candidate.sessionLeaseRuntimeKey() == targetKey {
				ownerID := candidate.ID
				a.mu.RUnlock()
				return a.SetActiveTab(ownerID)
			}
		}
		a.mu.RUnlock()
	}
	if ownerKind == sessionOwnerCurrentDetached {
		// Re-attach the detached runtime to this tab so its work becomes
		// visible again instead of staying hidden in the background.
		a.mu.RLock()
		root := tab.WorkspaceRoot
		path := tab.SessionPath
		a.mu.RUnlock()
		if root != "" && path != "" {
			a.goSafe("focusDetachedSession", func() {
				a.attachExistingSessionRuntime(tab, path, a.bootContext())
			})
		}
	}
	return nil
}

// reopenSessionCopy clones the authoritative session transcript (checkpoint
// plus event log, loaded under the source save lock) into a fresh path and
// rebuilds the tab against the copy. The original stays owned by its current
// holder, and the copy never competes for the same lease. The binding is
// re-verified atomically before the tab switches to the copy; a runtime that
// advanced meanwhile discards the new clone.
func (a *App) reopenSessionCopy(tab *WorkspaceTab, boundPath, boundEpoch string) error {
	if strings.TrimSpace(boundPath) == "" {
		return fmt.Errorf("no session path to copy")
	}
	a.mu.RLock()
	root := tab.WorkspaceRoot
	a.mu.RUnlock()
	dir := desktopSessionDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	copyPath := ""
	for i := 0; i < 3; i++ {
		candidate := agent.NewSessionPath(dir, "")
		if err := agent.CloneSessionToPath(boundPath, candidate); err != nil {
			if os.IsExist(err) {
				continue
			}
			return err
		}
		copyPath = candidate
		break
	}
	if copyPath == "" {
		return fmt.Errorf("create session copy: exhausted filename retries")
	}
	// Compare-and-apply: switch the tab to the copy only while the binding is
	// unchanged; otherwise the clone is stale and must be discarded.
	a.mu.Lock()
	currentPath := tab.SessionPath
	currentEpoch := a.sessionRuntimeViewLocked(tab).Epoch
	if currentPath != boundPath || currentEpoch != boundEpoch {
		a.mu.Unlock()
		_ = os.Remove(copyPath)
		return fmt.Errorf("session state advanced; copy discarded")
	}
	tab.SessionPath = copyPath
	clearTabStartupError(tab)
	a.mu.Unlock()
	a.goSafe("reopenSessionCopy", func() {
		a.buildTabControllerWithContext(tab, loadedTabSession{}, a.bootContext(), 0, nil)
	})
	return nil
}
