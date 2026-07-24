package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/control"
)

func TestMetaNeverReportsReadyWithoutController(t *testing.T) {
	app := NewApp()
	tab := &WorkspaceTab{
		ID:         "tab_broken_ready",
		Scope:      "global",
		Ready:      true, // compatibility field from an older persisted/runtime path
		StartupErr: "startup failed",
	}
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	meta := app.MetaForTab(tab.ID)
	if meta.Ready {
		t.Fatal("Meta reported ready with a nil controller")
	}
	if meta.Runtime.Phase != sessionRuntimeFailed {
		t.Fatalf("runtime phase = %q, want %q", meta.Runtime.Phase, sessionRuntimeFailed)
	}
}

func TestDeferredStartupReusesSameProcessTabLease(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "self-owned-startup.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	tab := app.createTabEntryWithID("global", globalTabWorkspaceRoot(), "", "self_owned")
	tab.SessionPath = path
	tab.StartupErr = (&sessionLeaseBusyError{}).Error()
	tab.StartupErrLeaseHeld = true
	tab.Ready = false
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	installNoopRuntimeEvents(app, tab.sink)
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	if err := tab.ensureSessionLease(path); err != nil {
		t.Fatalf("pre-acquire tab lease: %v", err)
	}
	t.Cleanup(func() {
		if ctrl := app.controllerForTab(tab); ctrl != nil {
			ctrl.Close()
		}
		tab.releaseSessionLease()
	})
	app.mu.Lock()
	app.bindSessionRuntimeKeyLocked(tab, path)
	app.setSessionRuntimePhaseLocked(tab, sessionRuntimeLeaseBlocked, &sessionLeaseBusyError{})
	app.mu.Unlock()

	before := tab.buildGeneration
	app.retryDeferredStartupBuild(tab.ID, tab)
	if tab.Ctrl == nil {
		t.Fatal("same-process owner was not rebuilt using its existing lease")
	}
	if tab.buildGeneration != before+1 {
		t.Fatalf("build generation = %d, want exactly one retry from %d", tab.buildGeneration, before)
	}
	app.mu.RLock()
	view := app.sessionRuntimeViewLocked(tab)
	runtimeCount := len(app.runtimeBySessionKey)
	app.mu.RUnlock()
	if view.Phase != sessionRuntimeReady {
		t.Fatalf("runtime phase = %q, want ready", view.Phase)
	}
	if runtimeCount != 1 {
		t.Fatalf("runtime key count = %d, want one", runtimeCount)
	}
}

func TestStartingRuntimePlaceholderIsNotOverwrittenByDuplicateTab(t *testing.T) {
	app := NewApp()
	path := filepath.Join(t.TempDir(), "same-session.jsonl")
	first := &WorkspaceTab{ID: "first", SessionPath: path}
	second := &WorkspaceTab{ID: "second", SessionPath: path}
	app.tabs[first.ID] = first
	app.tabs[second.ID] = second

	app.mu.Lock()
	app.setSessionRuntimePhaseLocked(first, sessionRuntimeStarting, nil)
	app.setSessionRuntimePhaseLocked(second, sessionRuntimeStarting, nil)
	key := sessionRuntimeKey(path)
	owner := app.runtimeBySessionKey[key]
	runtimeCount := len(app.runtimeBySessionKey)
	secondRuntimeID := second.runtimeID
	app.mu.Unlock()

	if owner == nil || owner.Owner != first {
		t.Fatalf("starting placeholder owner = %#v, want first tab", owner)
	}
	if runtimeCount != 1 {
		t.Fatalf("runtime key count = %d, want one", runtimeCount)
	}
	if secondRuntimeID != "" {
		t.Fatalf("duplicate tab created private runtime %q before claim", secondRuntimeID)
	}
}

func TestBackendRejectsSubmitWhenRuntimeIsNotReady(t *testing.T) {
	app := NewApp()
	ctrl := &runtimeStatusSessionController{}
	tab := &WorkspaceTab{ID: "blocked", Ctrl: ctrl, Ready: true}
	app.tabs[tab.ID] = tab
	app.activeTabID = tab.ID
	app.mu.Lock()
	app.setSessionRuntimePhaseLocked(tab, sessionRuntimeLeaseBlocked, &sessionLeaseBusyError{})
	app.mu.Unlock()

	err := app.SubmitToTab(tab.ID, "must not send")
	if err == nil || !strings.Contains(err.Error(), "workspace failed to start") {
		t.Fatalf("SubmitToTab error = %v, want runtime readiness failure", err)
	}
	if status := ctrl.RuntimeStatus(); status != (control.RuntimeStatus{}) {
		t.Fatalf("controller status changed after rejected submit: %+v", status)
	}
}
