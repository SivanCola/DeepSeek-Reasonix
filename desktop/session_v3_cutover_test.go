package main

import (
	"context"
	"encoding/json"
	"testing"

	"reasonix/internal/boot"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/sessionv3"
)

func appendV3TestMessage(t *testing.T, runtime *sessionv3.Runtime, operationID string, message provider.Message) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"message": message})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().AppendBatch(t.Context(), operationID, []sessionv3.Event{{Kind: "message/complete", Payload: payload}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func appendV3TestModel(t *testing.T, runtime *sessionv3.Runtime, operationID, modelRef string) {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"modelRef": modelRef})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().AppendBatch(t.Context(), operationID, []sessionv3.Event{{Kind: "session/config", Payload: payload}}); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopV3CatalogResumeRenameAndDeleteUseSessionIdentity(t *testing.T) {
	isolateDesktopUserDirs(t)
	model, targetModel := configureSwitchableDefaultModels(t)
	app := NewApp()
	app.ctx = context.Background()
	root := t.TempDir()
	dir := desktopSessionDir(root)
	service := app.desktopSessionService(dir)

	first, err := service.Create(t.Context(), sessionv3.CreateOptions{SessionID: "first-v3"})
	if err != nil {
		t.Fatal(err)
	}
	appendV3TestModel(t, first, "first-model", model)
	appendV3TestMessage(t, first, "first-message", provider.Message{ID: "user-first", Role: provider.RoleUser, Content: "first conversation"})
	second, err := service.Create(t.Context(), sessionv3.CreateOptions{SessionID: "second-v3"})
	if err != nil {
		t.Fatal(err)
	}
	appendV3TestModel(t, second, "second-model", targetModel)
	appendV3TestMessage(t, second, "second-message", provider.Message{ID: "user-second", Role: provider.RoleUser, Content: "second conversation"})

	ctrl, err := app.buildTabControllerBoot(app.ctx, boot.Options{Model: model, WorkspaceRoot: root, SessionDir: dir, Sink: event.Discard})
	if err != nil {
		t.Fatal(err)
	}
	identity := ctrl.(control.IdentityLifecycle)
	if _, err := identity.OpenV3(t.Context(), first.Ref()); err != nil {
		t.Fatal(err)
	}
	tab := &WorkspaceTab{ID: "v3-tab", Scope: "project", WorkspaceRoot: root, SessionID: first.Ref().SessionID, Ready: true, Ctrl: ctrl, sink: &tabEventSink{tabID: "v3-tab", app: app}, disabledMCP: map[string]ServerView{}}
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	app.newSessionRuntimeLocked(tab, sessionRuntimeKey(tab.currentSessionIdentity()))
	t.Cleanup(func() {
		if live := app.controllerForTab(tab); live != nil {
			live.Close()
		}
	})

	rows := app.ListSessions()
	if len(rows) != 2 || rows[0].SessionID == "" || rows[1].SessionID == "" {
		t.Fatalf("v3 catalog rows = %+v", rows)
	}
	if _, err := app.ResumeSessionForTab(tab.ID, sessionV3Route(second.Ref().SessionID)); err != nil {
		t.Fatal(err)
	}
	if tab.SessionID != second.Ref().SessionID || tab.SessionPath != "" {
		t.Fatalf("resumed identity = id %q path %q", tab.SessionID, tab.SessionPath)
	}
	if tab.Ctrl == ctrl || tab.Ctrl.ModelRef() != targetModel {
		t.Fatalf("target model runtime = ctrl changed %v model %q, want true/%q", tab.Ctrl != ctrl, tab.Ctrl.ModelRef(), targetModel)
	}
	if got := tab.Ctrl.History(); len(got) != 1 || got[0].Content != "second conversation" {
		t.Fatalf("resumed history = %+v", got)
	}
	if err := app.RenameSession(sessionV3Route(second.Ref().SessionID), "renamed v3"); err != nil {
		t.Fatal(err)
	}
	if snap, err := service.Query().Snapshot(t.Context(), second.Ref()); err != nil || snap.Projection.Title != "renamed v3" {
		t.Fatalf("renamed snapshot = %+v, %v", snap, err)
	}
	if err := app.DeleteSession(sessionV3Route(second.Ref().SessionID)); err != nil {
		t.Fatal(err)
	}
	if tab.SessionID == second.Ref().SessionID || tab.SessionPath != "" {
		t.Fatalf("delete did not rotate to a fresh identity: %+v", tab)
	}
}

func TestDesktopV3ResumeModelBuildFailureKeepsSourceRuntime(t *testing.T) {
	isolateDesktopUserDirs(t)
	model, _ := configureSwitchableDefaultModels(t)
	app := NewApp()
	app.ctx = context.Background()
	root := t.TempDir()
	dir := desktopSessionDir(root)
	service := app.desktopSessionService(dir)

	source, err := service.Create(t.Context(), sessionv3.CreateOptions{SessionID: "resume-source"})
	if err != nil {
		t.Fatal(err)
	}
	appendV3TestModel(t, source, "source-model", model)
	appendV3TestMessage(t, source, "source-message", provider.Message{ID: "source-user", Role: provider.RoleUser, Content: "source remains"})
	target, err := service.Create(t.Context(), sessionv3.CreateOptions{SessionID: "resume-broken-target"})
	if err != nil {
		t.Fatal(err)
	}
	appendV3TestModel(t, target, "target-model", "missing/model")
	appendV3TestMessage(t, target, "target-message", provider.Message{ID: "target-user", Role: provider.RoleUser, Content: "must not publish"})

	ctrl, err := app.buildTabControllerBoot(app.ctx, boot.Options{Model: model, WorkspaceRoot: root, SessionDir: dir, Sink: event.Discard})
	if err != nil {
		t.Fatal(err)
	}
	identity := ctrl.(control.IdentityLifecycle)
	if _, err := identity.OpenV3(t.Context(), source.Ref()); err != nil {
		t.Fatal(err)
	}
	tab := &WorkspaceTab{ID: "source-tab", Scope: "project", WorkspaceRoot: root, SessionID: source.Ref().SessionID, Ready: true, Ctrl: ctrl, model: model, sink: &tabEventSink{tabID: "source-tab", app: app}, disabledMCP: map[string]ServerView{}}
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	app.newSessionRuntimeLocked(tab, sessionRuntimeKey(tab.currentSessionIdentity()))
	t.Cleanup(func() {
		if live := app.controllerForTab(tab); live != nil {
			live.Close()
		}
	})

	if _, err := app.ResumeSessionForTab(tab.ID, sessionV3Route(target.Ref().SessionID)); err == nil {
		t.Fatal("resume with an unavailable target model unexpectedly succeeded")
	}
	if tab.Ctrl != ctrl || tab.SessionID != source.Ref().SessionID || tab.SessionPath != "" {
		t.Fatalf("failed resume changed source binding: ctrl=%v session=%q path=%q", tab.Ctrl == ctrl, tab.SessionID, tab.SessionPath)
	}
	if got := tab.Ctrl.History(); len(got) != 1 || got[0].Content != "source remains" {
		t.Fatalf("failed resume changed source history: %+v", got)
	}
}
