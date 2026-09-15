package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/boot"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/session"
)

type activeNavigationController struct {
	control.SessionAPI
	control.IdentityLifecycle
	status  control.RuntimeStatus
	closed  bool
	replays int
}

func (c *activeNavigationController) RuntimeStatus() control.RuntimeStatus { return c.status }
func (c *activeNavigationController) Close()                               { c.closed = true; c.SessionAPI.Close() }

func (c *activeNavigationController) ReplayPendingPrompts() {
	c.replays++
	c.SessionAPI.ReplayPendingPrompts()
}

func (c *activeNavigationController) SessionBinding() (*session.Service, *session.Runtime, bool) {
	return c.SessionAPI.(persistedSessionBinding).SessionBinding()
}

type snapshotProbeController struct {
	activeNavigationController
	snapshot func() error
}

func (c *snapshotProbeController) Snapshot() error {
	if c.snapshot != nil {
		return c.snapshot()
	}
	return c.SessionAPI.Snapshot()
}

func navigationFreezeFixture(t *testing.T) (*App, *WorkspaceTab, string) {
	t.Helper()
	isolateDesktopUserDirs(t)
	model, _ := configureSwitchableDefaultModels(t)
	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	app.ctx = context.Background()
	rootA, rootB := filepath.Join(t.TempDir(), "project"), filepath.Join(t.TempDir(), "project")
	for _, root := range []string{rootA, rootB} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	workspaceA, err := app.ensureDesktopWorkspace(t.Context(), "project", rootA)
	if err != nil {
		t.Fatal(err)
	}
	service := app.desktopSessionService("")
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: "session-A", CWD: rootA, Origin: session.SessionOriginNew})
	if err != nil {
		t.Fatal(err)
	}
	appendSessionTestModel(t, runtime, "session-A-model", model)
	appendSessionTestMessage(t, runtime, "session-A-system", provider.Message{ID: "session-A-system", Role: provider.RoleSystem, Content: "test system"})
	appendSessionTestMessage(t, runtime, "session-A-message", provider.Message{ID: "session-A-user", Role: provider.RoleUser, Content: "session-A"})
	if err := app.workspaceRegistry().AttachSession(t.Context(), "", workspaceA, runtime.Ref().SessionID, ""); err != nil {
		t.Fatal(err)
	}
	ctrl, err := app.buildTabControllerBoot(app.ctx, boot.Options{Model: model, WorkspaceRoot: rootA, SessionDir: desktopSessionDir(rootA), Sink: event.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ctrl.(control.IdentityLifecycle).OpenSession(t.Context(), runtime.Ref()); err != nil {
		t.Fatal(err)
	}
	tab := &WorkspaceTab{ID: "source-tab", Scope: "project", WorkspaceRoot: rootA, SessionID: runtime.Ref().SessionID, Ready: true, Ctrl: ctrl, model: model, sink: &tabEventSink{tabID: "source-tab", app: app}, disabledMCP: map[string]ServerView{}}
	tab.SessionWorkspace.ID = workspaceA
	app.tabs[tab.ID], app.tabOrder, app.activeTabID = tab, []string{tab.ID}, tab.ID
	app.newSessionRuntimeLocked(tab, sessionRuntimeKey(tab.currentSessionIdentity()))
	t.Cleanup(func() {
		if live := app.controllerForTab(tab); live != nil {
			live.Close()
		}
		if tab.SharedHostKey != "" {
			app.releaseSharedHost(tab.SharedHostKey)
		}
	})
	return app, tab, rootB
}

func TestPruneRunningSessionDoesNotSnapshot(t *testing.T) {
	app, tab, rootB := navigationFreezeFixture(t)
	original := tab.Ctrl
	snapped := false
	tab.TopicID = "topic-a"
	tab.Ctrl = &snapshotProbeController{
		activeNavigationController: activeNavigationController{
			SessionAPI: original, IdentityLifecycle: original.(control.IdentityLifecycle),
			status: control.RuntimeStatus{Running: true},
		},
		snapshot: func() error {
			snapped = true
			return original.Snapshot()
		},
	}
	t.Cleanup(original.Close)
	if _, err := app.EnsureBlankSurface("project", rootB); err != nil {
		t.Fatal(err)
	}
	if snapped {
		t.Fatal("switching away snapshotted a running session")
	}
}

func TestActivateTopicReattachesDetachedRunningSession(t *testing.T) {
	app, tab, rootB := navigationFreezeFixture(t)
	rootA := tab.WorkspaceRoot
	sourceID := tab.SessionID
	original := tab.Ctrl
	active := &activeNavigationController{
		SessionAPI: original, IdentityLifecycle: original.(control.IdentityLifecycle),
		status: control.RuntimeStatus{Running: true},
	}
	tab.Ctrl = active
	tab.TopicID = "topic-a"
	t.Cleanup(original.Close)
	if _, err := app.EnsureBlankSurface("project", rootB); err != nil {
		t.Fatal(err)
	}
	meta, err := app.ActivateTopic("project", rootA, "topic-a", "")
	if err != nil {
		t.Fatal(err)
	}
	visible := app.tabs[meta.ID]
	if visible == nil || visible.Ctrl != active || active.closed {
		t.Fatal("ActivateTopic did not reattach the running owner")
	}
	if visible.SessionID != sourceID {
		t.Fatalf("reattached session %q, want %q", visible.SessionID, sourceID)
	}
}

func TestBlankOnOtherProjectThenOpenSessionReattaches(t *testing.T) {
	app, tab, rootB := navigationFreezeFixture(t)
	source := session.SessionRef{HostID: localDesktopHostID, SessionID: tab.SessionID}
	original := tab.Ctrl
	active := &activeNavigationController{
		SessionAPI: original, IdentityLifecycle: original.(control.IdentityLifecycle),
		status: control.RuntimeStatus{Running: true},
	}
	tab.Ctrl = active
	t.Cleanup(original.Close)
	if _, err := app.EnsureBlankSurface("project", rootB); err != nil {
		t.Fatal(err)
	}
	if _, err := app.OpenSession(source); err != nil {
		t.Fatal(err)
	}
	visible := app.tabs[app.activeTabID]
	if visible == nil || visible.Ctrl != active || active.closed {
		t.Fatal("OpenSession did not reattach the running owner")
	}
}
