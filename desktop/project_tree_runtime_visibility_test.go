package main

import (
	"context"
	"os"
	"testing"
	"time"

	"reasonix/internal/control"
)

func TestKeepOnlyVisibleTabPublishesDetachedRuntimeToProjectTreeV2(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectA := t.TempDir()
	projectB := t.TempDir()
	for _, dir := range []string{desktopSessionDir(projectA), desktopSessionDir(projectB)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir session dir: %v", err)
		}
	}
	sessionA := writeTopicSessionWithPrompt(
		t,
		desktopSessionDir(projectA),
		"running-a.jsonl",
		"topic-a",
		"Running A",
		projectA,
		"keep working in project A",
		time.Now(),
	)
	sessionB := writeTopicSessionWithPrompt(
		t,
		desktopSessionDir(projectB),
		"visible-b.jsonl",
		"topic-b",
		"Visible B",
		projectB,
		"switch to project B",
		time.Now(),
	)

	app := NewApp()
	app.ctx = context.Background()
	events := make(chan runtimeEventEnvelope, 8)
	app.runtimeEvents.emit = func(ctx context.Context, name string, payload ...any) {
		events <- runtimeEventEnvelope{ctx: ctx, name: name, payload: append([]any(nil), payload...)}
	}
	t.Cleanup(func() { app.shutdown(context.Background()) })

	runningCtrl := &activationStubController{sessionPath: sessionA}
	visibleCtrl := &activationStubController{sessionPath: sessionB, status: &control.RuntimeStatus{}}
	running := &WorkspaceTab{
		ID: "tab-a", Scope: "project", WorkspaceRoot: projectA,
		TopicID: "topic-a", TopicTitle: "Running A", SessionPath: sessionA,
		Ctrl: runningCtrl, Ready: true, disabledMCP: map[string]ServerView{},
	}
	visible := &WorkspaceTab{
		ID: "tab-b", Scope: "project", WorkspaceRoot: projectB,
		TopicID: "topic-b", TopicTitle: "Visible B", SessionPath: sessionB,
		Ctrl: visibleCtrl, Ready: true, disabledMCP: map[string]ServerView{},
	}
	running.sink = &tabEventSink{tabID: running.ID, app: app}
	visible.sink = &tabEventSink{tabID: visible.ID, app: app}
	app.tabs[running.ID] = running
	app.tabs[visible.ID] = visible
	app.tabOrder = []string{running.ID, visible.ID}
	app.activeTabID = running.ID

	if _, err := app.keepOnlyVisibleTab(visible.ID); err != nil {
		t.Fatalf("keepOnlyVisibleTab: %v", err)
	}
	app.mu.RLock()
	detached := app.detachedSessions[sessionRuntimeKey(sessionA)]
	app.mu.RUnlock()
	if detached != running || runningCtrl.closed.Load() {
		t.Fatal("project A's running session was not preserved as a detached runtime")
	}

	deadline := time.After(time.Second)
	for {
		select {
		case emitted := <-events:
			if emitted.name != "project-tree:changed-v2" {
				continue
			}
			if len(emitted.payload) != 1 {
				t.Fatalf("project-tree:changed-v2 payload count = %d, want 1", len(emitted.payload))
			}
			event, ok := emitted.payload[0].(ProjectTreeChangedV2)
			if !ok {
				t.Fatalf("project-tree:changed-v2 payload type = %T, want ProjectTreeChangedV2", emitted.payload[0])
			}
			if event.Roots == nil || event.Reason != "runtime" {
				t.Fatalf("project-tree:changed-v2 event = %+v, want a runtime broadcast with [] roots", event)
			}
			return
		case <-deadline:
			t.Fatal("detaching project A emitted no project-tree:changed-v2 refresh; the running conversation stays invisible")
		}
	}
}
