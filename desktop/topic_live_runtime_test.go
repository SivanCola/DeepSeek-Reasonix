package main

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestStartTopicActivationPrefersLiveRuntimeOverRepresentative(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	sessionDir := desktopSessionDir(projectRoot)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	oldPath := writeTopicSessionWithPrompt(t, sessionDir, "old.jsonl", "topic-b", "Topic B", projectRoot, "yesterday unsigned todos", time.Now().Add(-24*time.Hour))
	livePath := writeTopicSessionWithPrompt(t, sessionDir, "live.jsonl", "topic-b", "Topic B", projectRoot, "today live turn", time.Now())

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	events := newActivationEventRecorder(app)
	t.Cleanup(func() { app.shutdown(context.Background()) })

	stub := &activationStubController{sessionPath: livePath}
	live := &WorkspaceTab{
		ID:             "tab-live",
		Scope:          "project",
		WorkspaceRoot:  projectRoot,
		TopicID:        "topic-b",
		TopicTitle:     "Topic B",
		SessionPath:    livePath,
		Ctrl:           stub,
		Label:          "stub-model",
		Ready:          true,
		ActivityStatus: topicStatusPaused,
		disabledMCP:    map[string]ServerView{},
	}
	live.sink = &tabEventSink{tabID: live.ID, app: app}
	installNoopRuntimeEvents(app, live.sink)
	if err := live.ensureSessionLease(livePath); err != nil {
		t.Fatalf("ensureSessionLease live: %v", err)
	}
	app.detachedSessions[sessionRuntimeKey(livePath)] = live

	ticket, err := app.StartTopicActivation(TopicActivationRequest{
		Scope:         "project",
		WorkspaceRoot: projectRoot,
		TopicID:       "topic-b",
		SessionPath:   oldPath,
		RequestID:     "req-live",
	})
	if err != nil {
		t.Fatalf("StartTopicActivation: %v", err)
	}
	events.waitFor(t, activationEventFor("req-live", "ready"))

	app.mu.RLock()
	tab := app.tabs[ticket.TabID]
	app.mu.RUnlock()
	if tab == nil {
		t.Fatal("activated tab missing")
	}
	if tab.Ctrl != stub {
		t.Fatal("clicking the live topic row opened the catalog representative instead of attaching the live controller")
	}
	if sessionRuntimeKey(tab.currentSessionPath()) != sessionRuntimeKey(livePath) {
		t.Fatalf("session path = %q, want live %q", tab.currentSessionPath(), livePath)
	}
	if stub.closed.Load() {
		t.Fatal("live controller was closed while attaching")
	}
	if ticket.Meta.SessionPath != "" && sessionRuntimeKey(ticket.Meta.SessionPath) != sessionRuntimeKey(livePath) {
		t.Fatalf("ticket session path = %q, want live %q", ticket.Meta.SessionPath, livePath)
	}
}

func TestStartTopicActivationReadyDoesNotWaitForRebuildMutex(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	sessionDir := desktopSessionDir(projectRoot)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	pathA := writeTopicSession(t, sessionDir, "a.jsonl", "topic-a", "Topic A", projectRoot)
	oldB := writeTopicSessionWithPrompt(t, sessionDir, "b-old.jsonl", "topic-b", "Topic B", projectRoot, "old b", time.Now().Add(-time.Hour))
	liveB := writeTopicSessionWithPrompt(t, sessionDir, "b-live.jsonl", "topic-b", "Topic B", projectRoot, "live b", time.Now())

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	events := newActivationEventRecorder(app)
	t.Cleanup(func() { app.shutdown(context.Background()) })

	stubA := &activationStubController{sessionPath: pathA}
	tabA := &WorkspaceTab{
		ID:            "tab-a",
		Scope:         "project",
		WorkspaceRoot: projectRoot,
		TopicID:       "topic-a",
		TopicTitle:    "Topic A",
		SessionPath:   pathA,
		Ctrl:          stubA,
		Label:         "stub-a",
		Ready:         true,
		disabledMCP:   map[string]ServerView{},
	}
	tabA.sink = &tabEventSink{tabID: tabA.ID, app: app}
	installNoopRuntimeEvents(app, tabA.sink)
	if err := tabA.ensureSessionLease(pathA); err != nil {
		t.Fatalf("ensureSessionLease A: %v", err)
	}
	app.tabs[tabA.ID] = tabA
	app.tabOrder = []string{tabA.ID}
	app.activeTabID = tabA.ID

	stubB := &activationStubController{sessionPath: liveB}
	live := &WorkspaceTab{
		ID:            "tab-b",
		Scope:         "project",
		WorkspaceRoot: projectRoot,
		TopicID:       "topic-b",
		TopicTitle:    "Topic B",
		SessionPath:   liveB,
		Ctrl:          stubB,
		Label:         "stub-b",
		Ready:         true,
		disabledMCP:   map[string]ServerView{},
	}
	live.sink = &tabEventSink{tabID: live.ID, app: app}
	installNoopRuntimeEvents(app, live.sink)
	if err := live.ensureSessionLease(liveB); err != nil {
		t.Fatalf("ensureSessionLease B: %v", err)
	}
	app.detachedSessions[sessionRuntimeKey(liveB)] = live

	app.runtimeRebuildMu.Lock()
	defer app.runtimeRebuildMu.Unlock()

	started := time.Now()
	ticket, err := app.StartTopicActivation(TopicActivationRequest{
		Scope:         "project",
		WorkspaceRoot: projectRoot,
		TopicID:       "topic-b",
		SessionPath:   oldB,
		RequestID:     "req-rebuild",
	})
	if err != nil {
		t.Fatalf("StartTopicActivation: %v", err)
	}
	if time.Since(started) > 300*time.Millisecond {
		t.Fatalf("StartTopicActivation blocked %s while MCP rebuild held the mutex", time.Since(started))
	}
	deadline := time.After(400 * time.Millisecond)
	for {
		select {
		case ev := <-events.ch:
			if ev.RequestID == "req-rebuild" && ev.Phase == "ready" {
				if sessionRuntimeKey(ticket.Meta.SessionPath) != sessionRuntimeKey(liveB) {
					t.Fatalf("ticket path = %q, want live %q", ticket.Meta.SessionPath, liveB)
				}
				return
			}
		case <-deadline:
			t.Fatal("ready waited for keepOnlyVisibleTab to acquire the rebuild mutex")
		}
	}
}

func TestPreferLiveSessionPathKeepsExplicitNonRepresentativeInspect(t *testing.T) {
	live := "/sessions/live.jsonl"
	rep := "/sessions/rep.jsonl"
	inspect := "/sessions/inspect.jsonl"
	if got := preferLiveSessionPath(inspect, live, rep); sessionRuntimeKey(got) != sessionRuntimeKey(inspect) {
		t.Fatalf("inspect path = %q, want %q", got, inspect)
	}
	if got := preferLiveSessionPath(rep, live, rep); sessionRuntimeKey(got) != sessionRuntimeKey(live) {
		t.Fatalf("representative path = %q, want live %q", got, live)
	}
	if got := preferLiveSessionPath("", live, rep); sessionRuntimeKey(got) != sessionRuntimeKey(live) {
		t.Fatalf("empty path = %q, want live %q", got, live)
	}
	if got := preferLiveSessionPath(rep, "", rep); sessionRuntimeKey(got) != sessionRuntimeKey(rep) {
		t.Fatalf("no live runtime = %q, want representative", got)
	}
}
