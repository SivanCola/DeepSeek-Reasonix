package daemon

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

type daemonScriptedProvider struct {
	turns [][]provider.Chunk
}

func (p *daemonScriptedProvider) Name() string { return "daemon-test" }

func (p *daemonScriptedProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 8)
	turn := []provider.Chunk{{Type: provider.ChunkText, Text: "done\n\n[goal:complete]"}}
	if len(p.turns) > 0 {
		turn = p.turns[0]
		p.turns = p.turns[1:]
	}
	go func() {
		defer close(ch)
		for _, c := range turn {
			select {
			case <-ctx.Done():
				return
			case ch <- c:
			}
		}
		ch <- provider.Chunk{Type: provider.ChunkDone}
	}()
	return ch, nil
}

func TestDaemonStartAndStatus(t *testing.T) {
	dir := t.TempDir()

	d := New(Options{
		Addr:       "127.0.0.1:0",
		SessionDir: dir,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Test lock acquisition.
	if err := d.acquireLock(); err != nil {
		t.Fatalf("acquireLock: %v", err)
	}
	defer d.releaseLock()

	// Verify lockfile exists.
	if _, err := os.Stat(d.lockFile()); err != nil {
		t.Fatalf("lockfile not created: %v", err)
	}

	// Second acquire should fail.
	d2 := New(Options{SessionDir: dir})
	if err := d2.acquireLock(); err == nil {
		t.Fatal("expected second lock acquire to fail")
		d2.releaseLock()
	}

	_ = ctx
}

func TestDaemonScanSessions(t *testing.T) {
	dir := t.TempDir()

	sess1 := filepath.Join(dir, "session1.jsonl")
	os.WriteFile(sess1, []byte(`{"role":"user"}`), 0o644)
	agent.SaveRuntimeMeta(sess1, agent.RuntimeMeta{
		SessionID: "session1",
		Goal:      agent.RuntimeGoalMeta{Text: "goal 1", Status: "running"},
		Run:       agent.RuntimeRunMeta{Status: "idle"},
	})

	sess2 := filepath.Join(dir, "session2.jsonl")
	os.WriteFile(sess2, []byte(`{"role":"user"}`), 0o644)
	agent.SaveRuntimeMeta(sess2, agent.RuntimeMeta{
		SessionID: "session2",
		Goal:      agent.RuntimeGoalMeta{Text: "goal 2", Status: "blocked"},
		Run:       agent.RuntimeRunMeta{Status: "running"},
	})

	d := New(Options{SessionDir: dir})
	d.scanSessions()

	d.mu.RLock()
	defer d.mu.RUnlock()
	if len(d.registry) != 2 {
		t.Fatalf("registry has %d entries, want 2", len(d.registry))
	}
	if d.registry["session1"] == nil {
		t.Error("session1 not found in registry")
	}
	if d.registry["session2"] == nil {
		t.Error("session2 not found in registry")
	}
}

func TestDaemonRecoverInterrupted(t *testing.T) {
	dir := t.TempDir()

	sess := filepath.Join(dir, "crashed.jsonl")
	os.WriteFile(sess, []byte(`{"role":"user"}`), 0o644)
	agent.SaveRuntimeMeta(sess, agent.RuntimeMeta{
		SessionID: "crashed",
		Goal:      agent.RuntimeGoalMeta{Text: "ship it", Status: "running"},
		Run:       agent.RuntimeRunMeta{Status: "running"},
	})

	d := New(Options{SessionDir: dir})
	d.scanSessions()
	d.recoverInterrupted()

	d.mu.RLock()
	entry := d.registry["crashed"]
	d.mu.RUnlock()

	if entry == nil {
		t.Fatal("entry not found")
	}
	if entry.Runtime.Run.Status != "interrupted" {
		t.Errorf("Run.Status = %q, want 'interrupted'", entry.Runtime.Run.Status)
	}

	loaded, ok, err := agent.LoadRuntimeMeta(sess)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta: err=%v ok=%v", err, ok)
	}
	if loaded.Run.Status != "interrupted" {
		t.Errorf("persisted Run.Status = %q, want 'interrupted'", loaded.Run.Status)
	}
}

func TestDaemonRecoverWaitingApprovalAsInterrupted(t *testing.T) {
	dir := t.TempDir()

	sess := filepath.Join(dir, "waiting.jsonl")
	os.WriteFile(sess, []byte(`{"role":"user"}`), 0o644)
	agent.SaveRuntimeMeta(sess, agent.RuntimeMeta{
		SessionID: "waiting",
		Goal:      agent.RuntimeGoalMeta{Text: "ship it", Status: "running"},
		Run:       agent.RuntimeRunMeta{Status: "waiting_approval"},
		Wait:      agent.RuntimeWaitMeta{Kind: "approval", ApprovalID: "7", Tool: "bash"},
	})

	d := New(Options{SessionDir: dir})
	d.scanSessions()
	d.recoverInterrupted()

	loaded, ok, err := agent.LoadRuntimeMeta(sess)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta: err=%v ok=%v", err, ok)
	}
	if loaded.Run.Status != "interrupted" {
		t.Fatalf("Run.Status = %q, want interrupted", loaded.Run.Status)
	}
	if loaded.Wait.ApprovalID != "7" {
		t.Fatalf("wait metadata should be preserved, got %+v", loaded.Wait)
	}
	events, ok, err := agent.LoadRuntimeTimeline(sess, 1)
	if err != nil || !ok || len(events) != 1 {
		t.Fatalf("LoadRuntimeTimeline: events=%+v err=%v ok=%v", events, err, ok)
	}
	if events[0].Type != "run_interrupted" || events[0].WaitID != "7" {
		t.Fatalf("unexpected recovery timeline: %+v", events[0])
	}
}

func TestDaemonRecoverPreservesDaemonOwnedWaits(t *testing.T) {
	dir := t.TempDir()

	eventSess := filepath.Join(dir, "waiting-event.jsonl")
	os.WriteFile(eventSess, []byte(`{"role":"user"}`), 0o644)
	agent.SaveRuntimeMeta(eventSess, agent.RuntimeMeta{
		SessionID: "waiting-event",
		Goal:      agent.RuntimeGoalMeta{Text: "ship it", Status: "running"},
		Run:       agent.RuntimeRunMeta{Status: "waiting_event"},
		Wait:      agent.RuntimeWaitMeta{Kind: "event", EventSource: "github.workflow_run"},
	})

	timeSess := filepath.Join(dir, "waiting-time.jsonl")
	os.WriteFile(timeSess, []byte(`{"role":"user"}`), 0o644)
	agent.SaveRuntimeMeta(timeSess, agent.RuntimeMeta{
		SessionID: "waiting-time",
		Goal:      agent.RuntimeGoalMeta{Text: "ship it", Status: "running"},
		Run:       agent.RuntimeRunMeta{Status: "waiting_time"},
		Wait:      agent.RuntimeWaitMeta{Kind: "time", Until: time.Now().Add(time.Hour).UTC()},
	})

	fileSess := filepath.Join(dir, "waiting-file.jsonl")
	os.WriteFile(fileSess, []byte(`{"role":"user"}`), 0o644)
	agent.SaveRuntimeMeta(fileSess, agent.RuntimeMeta{
		SessionID: "waiting-file",
		Goal:      agent.RuntimeGoalMeta{Text: "ship it", Status: "running"},
		Run:       agent.RuntimeRunMeta{Status: "waiting_file"},
		Wait:      agent.RuntimeWaitMeta{Kind: "file", FilePaths: []string{"src/a.go"}},
	})

	d := New(Options{SessionDir: dir})
	d.scanSessions()
	d.recoverInterrupted()

	eventMeta, ok, err := agent.LoadRuntimeMeta(eventSess)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta event: err=%v ok=%v", err, ok)
	}
	if eventMeta.Run.Status != "waiting_event" || eventMeta.Wait.Kind != "event" {
		t.Fatalf("event wait should be preserved: run=%+v wait=%+v", eventMeta.Run, eventMeta.Wait)
	}
	timeMeta, ok, err := agent.LoadRuntimeMeta(timeSess)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta time: err=%v ok=%v", err, ok)
	}
	if timeMeta.Run.Status != "waiting_time" || timeMeta.Wait.Kind != "time" {
		t.Fatalf("time wait should be preserved: run=%+v wait=%+v", timeMeta.Run, timeMeta.Wait)
	}
	fileMeta, ok, err := agent.LoadRuntimeMeta(fileSess)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta file: err=%v ok=%v", err, ok)
	}
	if fileMeta.Run.Status != "waiting_file" || fileMeta.Wait.Kind != "file" {
		t.Fatalf("file wait should be preserved: run=%+v wait=%+v", fileMeta.Run, fileMeta.Wait)
	}
}

func TestDaemonHTTPHandlers(t *testing.T) {
	dir := t.TempDir()

	sess := filepath.Join(dir, "api-test.jsonl")
	os.WriteFile(sess, []byte(`{"role":"user"}`), 0o644)
	agent.SaveRuntimeMeta(sess, agent.RuntimeMeta{
		SessionID: "api-test",
		Goal:      agent.RuntimeGoalMeta{Text: "test goal", Status: "running"},
		Run:       agent.RuntimeRunMeta{Status: "idle"},
	})

	d := New(Options{
		Addr:       "127.0.0.1:0",
		SessionDir: dir,
	})
	d.scanSessions()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", d.handleStatus)
	mux.HandleFunc("GET /sessions", d.handleSessions)
	mux.HandleFunc("POST /continue-goal", d.handleContinueGoal)
	mux.HandleFunc("POST /stop", d.handleStop)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	addr := ln.Addr().String()
	client := &http.Client{Timeout: 2 * time.Second}

	// GET /status
	resp, err := client.Get("http://" + addr + "/status")
	if err != nil {
		t.Fatalf("GET /status: %v", err)
	}
	var status StatusResponse
	json.NewDecoder(resp.Body).Decode(&status)
	resp.Body.Close()
	if status.Status != "running" {
		t.Errorf("status = %q, want running", status.Status)
	}
	if status.Sessions != 1 {
		t.Errorf("sessions = %d, want 1", status.Sessions)
	}

	// GET /sessions
	resp, err = client.Get("http://" + addr + "/sessions")
	if err != nil {
		t.Fatalf("GET /sessions: %v", err)
	}
	var sessions SessionsResponse
	json.NewDecoder(resp.Body).Decode(&sessions)
	resp.Body.Close()
	if len(sessions.Sessions) != 1 {
		t.Fatalf("sessions count = %d, want 1", len(sessions.Sessions))
	}
	if sessions.Sessions[0].GoalText != "test goal" {
		t.Errorf("goal text = %q", sessions.Sessions[0].GoalText)
	}

	// POST /stop
	resp, err = client.Post("http://"+addr+"/stop", "application/json",
		strings.NewReader(`{"session_id":"api-test"}`))
	if err != nil {
		t.Fatalf("POST /stop: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("stop status = %d", resp.StatusCode)
	}

	// Verify the session was stopped.
	d.mu.RLock()
	entry := d.registry["api-test"]
	d.mu.RUnlock()
	if entry.Runtime.Run.Status != "stopped" {
		t.Errorf("after stop: Run.Status = %q", entry.Runtime.Run.Status)
	}
}

func TestDaemonTimelineHandler(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "timeline-api.jsonl")
	os.WriteFile(sess, []byte(`{"role":"user","content":"start"}`+"\n"), 0o644)
	agent.SaveRuntimeMeta(sess, agent.RuntimeMeta{
		SessionID: "timeline-api",
		Goal:      agent.RuntimeGoalMeta{Text: "timeline goal", Status: "running"},
		Run:       agent.RuntimeRunMeta{Status: "idle"},
	})
	if err := agent.AppendRuntimeTimeline(sess, agent.RuntimeTimelineEvent{Type: "intent_queued", Source: "test"}); err != nil {
		t.Fatalf("AppendRuntimeTimeline: %v", err)
	}

	d := New(Options{SessionDir: dir})
	d.scanSessions()
	req := httptest.NewRequest("GET", "/timeline?session_id=timeline-api&limit=10", nil)
	rr := httptest.NewRecorder()
	d.handleTimeline(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("timeline status = %d body=%s", rr.Code, rr.Body.String())
	}
	var resp TimelineResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}
	if resp.SessionID != "timeline-api" || len(resp.Events) != 1 || resp.Events[0].Type != "intent_queued" {
		t.Fatalf("unexpected timeline response: %+v", resp)
	}
}

func TestDaemonWatchHandlerPersistsConfig(t *testing.T) {
	dir := t.TempDir()
	watchDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sess := filepath.Join(dir, "watch-api.jsonl")
	if err := os.WriteFile(sess, []byte(`{"role":"user","content":"start"}`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := agent.SaveRuntimeMeta(sess, agent.RuntimeMeta{
		SessionID: "watch-api",
		Goal:      agent.RuntimeGoalMeta{Text: "watch files", Status: control.GoalStatusRunning},
		Run:       agent.RuntimeRunMeta{Status: "idle"},
	}); err != nil {
		t.Fatalf("SaveRuntimeMeta: %v", err)
	}

	d := New(Options{SessionDir: dir})
	d.scanSessions()
	d.fileWatcher = NewFileWatcher(d, nil)

	body := `{"session_id":"watch-api","paths":["` + watchDir + `"],"ignore_patterns":["*.tmp"],"debounce":"5s","enabled":true}`
	req := httptest.NewRequest("POST", "/watch", strings.NewReader(body))
	rr := httptest.NewRecorder()
	d.handleWatch(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("watch status = %d body=%s", rr.Code, rr.Body.String())
	}

	loaded, ok, err := agent.LoadRuntimeMeta(sess)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta: err=%v ok=%v", err, ok)
	}
	if !loaded.FileWatch.Enabled || len(loaded.FileWatch.Paths) != 1 || loaded.FileWatch.Paths[0] != watchDir {
		t.Fatalf("file watch config not persisted: %+v", loaded.FileWatch)
	}
	if loaded.FileWatch.Debounce != 5*time.Second {
		t.Fatalf("Debounce = %v, want 5s", loaded.FileWatch.Debounce)
	}
	d.fileWatcher.mu.Lock()
	registered := d.fileWatcher.watches["watch-api"]
	d.fileWatcher.mu.Unlock()
	if registered == nil || !registered.config.Enabled || len(registered.config.Paths) != 1 {
		t.Fatalf("file watcher not registered: %+v", registered)
	}
	events, ok, err := agent.LoadRuntimeTimeline(sess, 1)
	if err != nil || !ok || len(events) != 1 || events[0].Type != "watch_configured" {
		t.Fatalf("watch timeline not recorded: events=%+v err=%v ok=%v", events, err, ok)
	}
}

func TestDaemonBudgetHandlerPersistsConfig(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "budget-api.jsonl")
	if err := os.WriteFile(sess, []byte(`{"role":"user","content":"start"}`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := agent.SaveRuntimeMeta(sess, agent.RuntimeMeta{
		SessionID: "budget-api",
		Goal:      agent.RuntimeGoalMeta{Text: "keep budget", Status: control.GoalStatusRunning},
		Run:       agent.RuntimeRunMeta{Status: "idle"},
		Budget: agent.RuntimeBudgetMeta{
			DailyWakeups:      3,
			LastBlockedReason: "old",
		},
	}); err != nil {
		t.Fatalf("SaveRuntimeMeta: %v", err)
	}

	d := New(Options{SessionDir: dir})
	d.scanSessions()
	req := httptest.NewRequest("POST", "/budget", strings.NewReader(`{"session_id":"budget-api","daily_wakeup_limit":5,"reset":true}`))
	rr := httptest.NewRecorder()
	d.handleBudget(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("budget status = %d body=%s", rr.Code, rr.Body.String())
	}

	loaded, ok, err := agent.LoadRuntimeMeta(sess)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta: err=%v ok=%v", err, ok)
	}
	if loaded.Budget.DailyWakeupLimit != 5 || loaded.Budget.DailyWakeups != 0 || loaded.Budget.LastBlockedReason != "" {
		t.Fatalf("budget not persisted/reset: %+v", loaded.Budget)
	}
	if loaded.Budget.WindowStartedAt.IsZero() {
		t.Fatal("budget reset should stamp WindowStartedAt")
	}
	events, ok, err := agent.LoadRuntimeTimeline(sess, 1)
	if err != nil || !ok || len(events) != 1 || events[0].Type != "budget_configured" {
		t.Fatalf("budget timeline not recorded: events=%+v err=%v ok=%v", events, err, ok)
	}
}

func TestDaemonWaitEventHandlerPersistsConfig(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "wait-event-api.jsonl")
	if err := os.WriteFile(sess, []byte(`{"role":"user","content":"start"}`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := agent.SaveRuntimeMeta(sess, agent.RuntimeMeta{
		SessionID: "wait-event-api",
		Goal:      agent.RuntimeGoalMeta{Text: "wait for CI", Status: control.GoalStatusRunning},
		Run:       agent.RuntimeRunMeta{Status: "idle"},
	}); err != nil {
		t.Fatalf("SaveRuntimeMeta: %v", err)
	}

	d := New(Options{SessionDir: dir})
	d.scanSessions()
	req := httptest.NewRequest("POST", "/wait-event", strings.NewReader(`{"session_id":"wait-event-api","event_source":"github.workflow_run","event_id":"delivery-42","event_status":"completed","event_conclusion":"success","reason":"waiting for CI","subject":"PR #42"}`))
	rr := httptest.NewRecorder()
	d.handleWaitEvent(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("wait-event status = %d body=%s", rr.Code, rr.Body.String())
	}

	loaded, ok, err := agent.LoadRuntimeMeta(sess)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta: err=%v ok=%v", err, ok)
	}
	if loaded.Run.Status != "waiting_event" {
		t.Fatalf("Run.Status = %q, want waiting_event", loaded.Run.Status)
	}
	if loaded.Wait.Kind != "event" || loaded.Wait.EventSource != "github.workflow_run" || loaded.Wait.EventID != "delivery-42" {
		t.Fatalf("wait condition not persisted: %+v", loaded.Wait)
	}
	if loaded.Wait.EventStatus != "completed" || loaded.Wait.EventConclusion != "success" {
		t.Fatalf("wait event status not persisted: %+v", loaded.Wait)
	}
	if loaded.Wait.Reason != "waiting for CI" || loaded.Wait.Subject != "PR #42" || loaded.Wait.Since.IsZero() {
		t.Fatalf("wait metadata incomplete: %+v", loaded.Wait)
	}
	events, ok, err := agent.LoadRuntimeTimeline(sess, 1)
	if err != nil || !ok || len(events) != 1 || events[0].Type != "wait_started" {
		t.Fatalf("wait timeline not recorded: events=%+v err=%v ok=%v", events, err, ok)
	}

	req = httptest.NewRequest("POST", "/wait-event", strings.NewReader(`{"session_id":"wait-event-api","clear":true}`))
	rr = httptest.NewRecorder()
	d.handleWaitEvent(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("clear wait-event status = %d body=%s", rr.Code, rr.Body.String())
	}
	loaded, ok, err = agent.LoadRuntimeMeta(sess)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta after clear: err=%v ok=%v", err, ok)
	}
	if loaded.Run.Status != "idle" || loaded.Wait.Kind != "" {
		t.Fatalf("wait condition not cleared: run=%+v wait=%+v", loaded.Run, loaded.Wait)
	}
	events, ok, err = agent.LoadRuntimeTimeline(sess, 1)
	if err != nil || !ok || len(events) != 1 || events[0].Type != "wait_cleared" {
		t.Fatalf("wait clear timeline not recorded: events=%+v err=%v ok=%v", events, err, ok)
	}
}

func TestDaemonWaitTimeHandlerPersistsConfig(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "wait-time-api.jsonl")
	if err := os.WriteFile(sess, []byte(`{"role":"user","content":"start"}`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := agent.SaveRuntimeMeta(sess, agent.RuntimeMeta{
		SessionID: "wait-time-api",
		Goal:      agent.RuntimeGoalMeta{Text: "wait until later", Status: control.GoalStatusRunning},
		Run:       agent.RuntimeRunMeta{Status: "idle"},
	}); err != nil {
		t.Fatalf("SaveRuntimeMeta: %v", err)
	}

	d := New(Options{SessionDir: dir})
	d.scanSessions()
	req := httptest.NewRequest("POST", "/wait-time", strings.NewReader(`{"session_id":"wait-time-api","after":"1h","reason":"wait for release window","subject":"release"}`))
	rr := httptest.NewRecorder()
	d.handleWaitTime(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("wait-time status = %d body=%s", rr.Code, rr.Body.String())
	}

	loaded, ok, err := agent.LoadRuntimeMeta(sess)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta: err=%v ok=%v", err, ok)
	}
	if loaded.Run.Status != "waiting_time" || loaded.Wait.Kind != "time" {
		t.Fatalf("time wait not persisted: run=%+v wait=%+v", loaded.Run, loaded.Wait)
	}
	if loaded.Wait.Until.IsZero() || loaded.Wait.Reason != "wait for release window" || loaded.Wait.Subject != "release" {
		t.Fatalf("time wait metadata incomplete: %+v", loaded.Wait)
	}
	events, ok, err := agent.LoadRuntimeTimeline(sess, 1)
	if err != nil || !ok || len(events) != 1 || events[0].Type != "wait_started" {
		t.Fatalf("time wait timeline not recorded: events=%+v err=%v ok=%v", events, err, ok)
	}

	req = httptest.NewRequest("POST", "/wait-time", strings.NewReader(`{"session_id":"wait-time-api","clear":true}`))
	rr = httptest.NewRecorder()
	d.handleWaitTime(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("clear wait-time status = %d body=%s", rr.Code, rr.Body.String())
	}
	loaded, ok, err = agent.LoadRuntimeMeta(sess)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta after clear: err=%v ok=%v", err, ok)
	}
	if loaded.Run.Status != "idle" || loaded.Wait.Kind != "" {
		t.Fatalf("time wait not cleared: run=%+v wait=%+v", loaded.Run, loaded.Wait)
	}
}

func TestDaemonWaitFileHandlerPersistsConfig(t *testing.T) {
	dir := t.TempDir()
	watchDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(filepath.Join(watchDir, "src"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sess := filepath.Join(dir, "wait-file-api.jsonl")
	if err := os.WriteFile(sess, []byte(`{"role":"user","content":"start"}`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := agent.SaveRuntimeMeta(sess, agent.RuntimeMeta{
		SessionID:     "wait-file-api",
		WorkspaceRoot: watchDir,
		Goal:          agent.RuntimeGoalMeta{Text: "wait for generated file", Status: control.GoalStatusRunning},
		Run:           agent.RuntimeRunMeta{Status: "idle"},
	}); err != nil {
		t.Fatalf("SaveRuntimeMeta: %v", err)
	}

	d := New(Options{SessionDir: dir})
	d.scanSessions()
	d.fileWatcher = NewFileWatcher(d, nil)
	req := httptest.NewRequest("POST", "/wait-file", strings.NewReader(`{"session_id":"wait-file-api","paths":["src/output.txt"],"ignore_patterns":["*.tmp"],"debounce":"5s","reason":"waiting for generator","subject":"output.txt"}`))
	rr := httptest.NewRecorder()
	d.handleWaitFile(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("wait-file status = %d body=%s", rr.Code, rr.Body.String())
	}

	loaded, ok, err := agent.LoadRuntimeMeta(sess)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta: err=%v ok=%v", err, ok)
	}
	if loaded.Run.Status != "waiting_file" || loaded.Wait.Kind != "file" || loaded.Wait.FilePaths[0] != "src/output.txt" {
		t.Fatalf("file wait not persisted: run=%+v wait=%+v", loaded.Run, loaded.Wait)
	}
	if !loaded.FileWatch.Enabled || loaded.FileWatch.Paths[0] != "src" || loaded.FileWatch.Debounce != 5*time.Second {
		t.Fatalf("file watch not configured for wait: %+v", loaded.FileWatch)
	}
	d.fileWatcher.mu.Lock()
	registered := d.fileWatcher.watches["wait-file-api"]
	d.fileWatcher.mu.Unlock()
	wantPath := filepath.Join(watchDir, "src")
	if registered == nil || len(registered.config.Paths) != 1 || registered.config.Paths[0] != wantPath {
		t.Fatalf("file watcher not registered with resolved path: %+v", registered)
	}
	events, ok, err := agent.LoadRuntimeTimeline(sess, 1)
	if err != nil || !ok || len(events) != 1 || events[0].Type != "wait_started" {
		t.Fatalf("wait-file timeline not recorded: events=%+v err=%v ok=%v", events, err, ok)
	}

	req = httptest.NewRequest("POST", "/wait-file", strings.NewReader(`{"session_id":"wait-file-api","clear":true}`))
	rr = httptest.NewRecorder()
	d.handleWaitFile(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("clear wait-file status = %d body=%s", rr.Code, rr.Body.String())
	}
	loaded, ok, err = agent.LoadRuntimeMeta(sess)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta after clear: err=%v ok=%v", err, ok)
	}
	if loaded.Run.Status != "idle" || loaded.Wait.Kind != "" || loaded.FileWatch.Enabled {
		t.Fatalf("file wait not cleared: run=%+v wait=%+v watch=%+v", loaded.Run, loaded.Wait, loaded.FileWatch)
	}
	d.fileWatcher.mu.Lock()
	registered = d.fileWatcher.watches["wait-file-api"]
	d.fileWatcher.mu.Unlock()
	if registered != nil {
		t.Fatalf("file watcher should be unregistered after clear: %+v", registered)
	}
}

func TestDaemonRestoreFileWatches(t *testing.T) {
	dir := t.TempDir()
	watchDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sess := filepath.Join(dir, "watch-restore.jsonl")
	if err := os.WriteFile(sess, []byte(`{"role":"user","content":"start"}`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := agent.SaveRuntimeMeta(sess, agent.RuntimeMeta{
		SessionID:     "watch-restore",
		WorkspaceRoot: watchDir,
		Goal:          agent.RuntimeGoalMeta{Text: "watch files", Status: control.GoalStatusRunning},
		Run:           agent.RuntimeRunMeta{Status: "idle"},
		FileWatch: agent.RuntimeWatchMeta{
			Enabled:        true,
			Paths:          []string{"src"},
			IgnorePatterns: []string{"*.tmp"},
			Debounce:       4 * time.Second,
		},
	}); err != nil {
		t.Fatalf("SaveRuntimeMeta: %v", err)
	}

	d := New(Options{SessionDir: dir})
	d.scanSessions()
	d.fileWatcher = NewFileWatcher(d, nil)
	d.restoreFileWatches()

	d.fileWatcher.mu.Lock()
	registered := d.fileWatcher.watches["watch-restore"]
	d.fileWatcher.mu.Unlock()
	if registered == nil {
		t.Fatal("file watch not restored")
	}
	if registered.config.Debounce != 4*time.Second || registered.config.IgnorePatterns[0] != "*.tmp" {
		t.Fatalf("unexpected restored config: %+v", registered.config)
	}
	wantPath := filepath.Join(watchDir, "src")
	if len(registered.config.Paths) != 1 || registered.config.Paths[0] != wantPath {
		t.Fatalf("restored paths = %+v, want %q", registered.config.Paths, wantPath)
	}
}

func TestFileWatcherWakeupIncludesChangedFileSummary(t *testing.T) {
	dir := t.TempDir()
	watchDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(filepath.Join(watchDir, "src"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sess := filepath.Join(dir, "file-summary.jsonl")
	if err := os.WriteFile(sess, []byte(`{"role":"user","content":"start"}`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := agent.SaveRuntimeMeta(sess, agent.RuntimeMeta{
		SessionID: "file-summary",
		Goal:      agent.RuntimeGoalMeta{Text: "react to files", Status: control.GoalStatusRunning},
		Run:       agent.RuntimeRunMeta{Status: "idle"},
	}); err != nil {
		t.Fatalf("SaveRuntimeMeta: %v", err)
	}

	d := New(Options{SessionDir: dir})
	d.scanSessions()
	fw := NewFileWatcher(d, d.logger)
	state := &watchState{
		config: FileWatchConfig{
			Paths:   []string{watchDir},
			Enabled: true,
		},
		lastSeen: map[string]time.Time{},
		changes: map[string]struct{}{
			filepath.Join(watchDir, "src", "b.go"): {},
			filepath.Join(watchDir, "src", "a.go"): {},
		},
		pending: true,
	}

	fw.fireWakeup("file-summary", state, time.Now())

	select {
	case intent := <-d.intentCh:
		if intent.Source != "file_watch" || intent.Reason != "file_change" {
			t.Fatalf("unexpected intent: %+v", intent)
		}
		if !strings.Contains(intent.Context, "File watch detected 2 changed file(s)") ||
			!strings.Contains(intent.Context, "src/a.go") ||
			!strings.Contains(intent.Context, "src/b.go") {
			t.Fatalf("missing changed file summary:\n%s", intent.Context)
		}
		if strings.Contains(intent.Context, watchDir) {
			t.Fatalf("summary should prefer paths relative to watch root:\n%s", intent.Context)
		}
	default:
		t.Fatal("file watcher did not enqueue an intent")
	}

	events, ok, err := agent.LoadRuntimeTimeline(sess, 0)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeTimeline: err=%v ok=%v", err, ok)
	}
	var found bool
	for _, event := range events {
		if event.Type == "file_change_detected" && strings.Contains(event.Message, "src/a.go") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("file change timeline event missing: %+v", events)
	}
}

func TestFileWatcherWakeupClearsMatchingFileWait(t *testing.T) {
	dir := t.TempDir()
	watchDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(filepath.Join(watchDir, "src"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sess := filepath.Join(dir, "file-wait-match.jsonl")
	if err := os.WriteFile(sess, []byte(`{"role":"user","content":"start"}`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := agent.SaveRuntimeMeta(sess, agent.RuntimeMeta{
		SessionID: "file-wait-match",
		Goal:      agent.RuntimeGoalMeta{Text: "react to one file", Status: control.GoalStatusRunning},
		Run:       agent.RuntimeRunMeta{Status: "waiting_file"},
		Wait: agent.RuntimeWaitMeta{
			Kind:      "file",
			FilePaths: []string{"src/a.go"},
			Subject:   "src/a.go",
		},
		FileWatch: agent.RuntimeWatchMeta{
			Enabled: true,
			Paths:   []string{filepath.Join(watchDir, "src", "a.go")},
		},
	}); err != nil {
		t.Fatalf("SaveRuntimeMeta: %v", err)
	}

	d := New(Options{SessionDir: dir})
	d.scanSessions()
	fw := NewFileWatcher(d, d.logger)
	fw.Register("file-wait-match", FileWatchConfig{Paths: []string{filepath.Join(watchDir, "src", "a.go")}, Enabled: true})
	state := &watchState{
		config:   FileWatchConfig{Paths: []string{filepath.Join(watchDir, "src", "a.go")}, Enabled: true},
		lastSeen: map[string]time.Time{},
		changes: map[string]struct{}{
			filepath.Join(watchDir, "src", "a.go"): {},
		},
		pending: true,
	}

	fw.fireWakeup("file-wait-match", state, time.Now())

	select {
	case intent := <-d.intentCh:
		if intent.SessionID != "file-wait-match" || intent.Source != "file_watch" {
			t.Fatalf("unexpected intent: %+v", intent)
		}
	default:
		t.Fatal("matching file wait did not enqueue intent")
	}
	loaded, ok, err := agent.LoadRuntimeMeta(sess)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta: err=%v ok=%v", err, ok)
	}
	if loaded.Run.Status != "pending_continue" || loaded.Wait.Kind != "" || loaded.FileWatch.Enabled {
		t.Fatalf("matching file wait should clear wait and watcher: run=%+v wait=%+v watch=%+v", loaded.Run, loaded.Wait, loaded.FileWatch)
	}
	fw.mu.Lock()
	registered := fw.watches["file-wait-match"]
	fw.mu.Unlock()
	if registered != nil {
		t.Fatalf("matching file wait should unregister watcher: %+v", registered)
	}
}

func TestFileWatcherDoesNotWakeDifferentWaitKind(t *testing.T) {
	dir := t.TempDir()
	watchDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sess := filepath.Join(dir, "file-wait-event.jsonl")
	if err := os.WriteFile(sess, []byte(`{"role":"user","content":"start"}`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := agent.SaveRuntimeMeta(sess, agent.RuntimeMeta{
		SessionID: "file-wait-event",
		Goal:      agent.RuntimeGoalMeta{Text: "wait for CI", Status: control.GoalStatusRunning},
		Run:       agent.RuntimeRunMeta{Status: "waiting_event"},
		Wait:      agent.RuntimeWaitMeta{Kind: "event", EventSource: "github.workflow_run"},
	}); err != nil {
		t.Fatalf("SaveRuntimeMeta: %v", err)
	}

	d := New(Options{SessionDir: dir})
	d.scanSessions()
	fw := NewFileWatcher(d, d.logger)
	state := &watchState{
		config:   FileWatchConfig{Paths: []string{watchDir}, Enabled: true},
		lastSeen: map[string]time.Time{},
		changes: map[string]struct{}{
			filepath.Join(watchDir, "changed.md"): {},
		},
		pending: true,
	}
	fw.fireWakeup("file-wait-event", state, time.Now())

	select {
	case intent := <-d.intentCh:
		t.Fatalf("file watcher should not wake event wait: %+v", intent)
	default:
	}
	loaded, ok, err := agent.LoadRuntimeMeta(sess)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta: err=%v ok=%v", err, ok)
	}
	if loaded.Run.Status != "waiting_event" || loaded.Wait.Kind != "event" {
		t.Fatalf("event wait should remain unchanged: run=%+v wait=%+v", loaded.Run, loaded.Wait)
	}
}

func TestFileWatcherIgnoresNonMatchingFileWait(t *testing.T) {
	dir := t.TempDir()
	watchDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sess := filepath.Join(dir, "file-wait-miss.jsonl")
	if err := os.WriteFile(sess, []byte(`{"role":"user","content":"start"}`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := agent.SaveRuntimeMeta(sess, agent.RuntimeMeta{
		SessionID: "file-wait-miss",
		Goal:      agent.RuntimeGoalMeta{Text: "wait for one file", Status: control.GoalStatusRunning},
		Run:       agent.RuntimeRunMeta{Status: "waiting_file"},
		Wait:      agent.RuntimeWaitMeta{Kind: "file", FilePaths: []string{"wanted.txt"}},
	}); err != nil {
		t.Fatalf("SaveRuntimeMeta: %v", err)
	}

	d := New(Options{SessionDir: dir})
	d.scanSessions()
	fw := NewFileWatcher(d, d.logger)
	state := &watchState{
		config:   FileWatchConfig{Paths: []string{watchDir}, Enabled: true},
		lastSeen: map[string]time.Time{},
		changes: map[string]struct{}{
			filepath.Join(watchDir, "other.txt"): {},
		},
		pending: true,
	}
	fw.fireWakeup("file-wait-miss", state, time.Now())

	select {
	case intent := <-d.intentCh:
		t.Fatalf("non-matching file wait should not enqueue intent: %+v", intent)
	default:
	}
	loaded, ok, err := agent.LoadRuntimeMeta(sess)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta: err=%v ok=%v", err, ok)
	}
	if loaded.Run.Status != "waiting_file" || loaded.Wait.Kind != "file" {
		t.Fatalf("file wait should remain unchanged: run=%+v wait=%+v", loaded.Run, loaded.Wait)
	}
	events, ok, err := agent.LoadRuntimeTimeline(sess, 1)
	if err != nil || !ok || len(events) != 1 || events[0].Type != "wait_file_ignored" {
		t.Fatalf("file wait miss timeline not recorded: events=%+v err=%v ok=%v", events, err, ok)
	}
}

func TestFileWatcherWakeupRespectsDailyBudget(t *testing.T) {
	dir := t.TempDir()
	watchDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sess := filepath.Join(dir, "file-budget.jsonl")
	if err := os.WriteFile(sess, []byte(`{"role":"user","content":"start"}`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	now := time.Now().UTC()
	if err := agent.SaveRuntimeMeta(sess, agent.RuntimeMeta{
		SessionID: "file-budget",
		Goal:      agent.RuntimeGoalMeta{Text: "react to files", Status: control.GoalStatusRunning},
		Run:       agent.RuntimeRunMeta{Status: "idle"},
		Budget: agent.RuntimeBudgetMeta{
			DailyWakeupLimit: 1,
			DailyWakeups:     1,
			WindowStartedAt:  budgetWindowStart(now),
		},
	}); err != nil {
		t.Fatalf("SaveRuntimeMeta: %v", err)
	}

	d := New(Options{SessionDir: dir})
	d.scanSessions()
	fw := NewFileWatcher(d, d.logger)
	state := &watchState{
		config:   FileWatchConfig{Paths: []string{watchDir}, Enabled: true},
		lastSeen: map[string]time.Time{},
		changes: map[string]struct{}{
			filepath.Join(watchDir, "changed.md"): {},
		},
		pending: true,
	}
	fw.fireWakeup("file-budget", state, now)

	select {
	case intent := <-d.intentCh:
		t.Fatalf("budget-blocked file watch should not enqueue intent: %+v", intent)
	default:
	}
	loaded, ok, err := agent.LoadRuntimeMeta(sess)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta: err=%v ok=%v", err, ok)
	}
	if loaded.Run.Status != "idle" {
		t.Fatalf("Run.Status = %q, want idle", loaded.Run.Status)
	}
	if loaded.Budget.LastBlockedReason == "" || loaded.Scheduler.LastWakeupReason != "budget_blocked:file_watch" {
		t.Fatalf("budget block not persisted: budget=%+v scheduler=%+v", loaded.Budget, loaded.Scheduler)
	}
}

func TestDaemonExecuteIntentCompletesGoal(t *testing.T) {
	dir := t.TempDir()
	sessPath := filepath.Join(dir, "worker-test.jsonl")
	sess := agent.NewSession("")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "start"})
	if err := sess.Save(sessPath); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := agent.SaveRuntimeMeta(sessPath, agent.RuntimeMeta{
		SessionID: "worker-test",
		Goal:      agent.RuntimeGoalMeta{Text: "finish worker", Status: control.GoalStatusRunning},
		Run:       agent.RuntimeRunMeta{Status: "pending_continue"},
	}); err != nil {
		t.Fatalf("SaveRuntimeMeta: %v", err)
	}

	prov := &daemonScriptedProvider{}
	d := New(Options{
		SessionDir: dir,
		ControllerFactory: func(ctx context.Context, d *Daemon, entry *SessionEntry, sink event.Sink) (*control.Controller, error) {
			loaded, err := agent.LoadSession(entry.Path)
			if err != nil {
				return nil, err
			}
			ag := agent.New(prov, tool.NewRegistry(), loaded, agent.Options{}, sink)
			c := control.New(control.Options{
				Runner:      ag,
				Executor:    ag,
				SessionPath: entry.Path,
				SessionDir:  dir,
				Sink:        sink,
			})
			c.Resume(loaded, entry.Path)
			return c, nil
		},
	})
	d.scanSessions()
	d.executeIntent(context.Background(), RunIntent{SessionID: "worker-test", Source: "test", Reason: "test"})

	loaded, ok, err := agent.LoadRuntimeMeta(sessPath)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta: err=%v ok=%v", err, ok)
	}
	if loaded.Goal.Status != control.GoalStatusComplete {
		t.Fatalf("Goal.Status = %q, want complete", loaded.Goal.Status)
	}
	if loaded.Run.Status != "idle" {
		t.Fatalf("Run.Status = %q, want idle", loaded.Run.Status)
	}
}

func TestDaemonRecordWaitAndApproveClearsRuntime(t *testing.T) {
	dir := t.TempDir()
	sessPath := filepath.Join(dir, "approval-test.jsonl")
	os.WriteFile(sessPath, []byte(`{"role":"user","content":"start"}`+"\n"), 0o644)
	if err := agent.SaveRuntimeMeta(sessPath, agent.RuntimeMeta{
		SessionID: "approval-test",
		Goal:      agent.RuntimeGoalMeta{Text: "needs approval", Status: control.GoalStatusRunning},
		Run:       agent.RuntimeRunMeta{Status: "running"},
	}); err != nil {
		t.Fatalf("SaveRuntimeMeta: %v", err)
	}
	d := New(Options{SessionDir: dir})
	d.scanSessions()
	ctrl := control.New(control.Options{Sink: event.Discard})
	d.mu.Lock()
	d.activeRuns["approval-test"] = &ActiveRun{
		Control:   ctrl,
		Approvals: map[string]event.Approval{"42": {ID: "42", Tool: "bash", Subject: "go test ./..."}},
		Asks:      map[string]event.Ask{},
	}
	d.mu.Unlock()
	d.recordWait("approval-test", agent.RuntimeWaitMeta{
		Kind:       "approval",
		Reason:     "approval required",
		ApprovalID: "42",
		Tool:       "bash",
		Subject:    "go test ./...",
		Since:      time.Now().UTC(),
	}, event.Event{Kind: event.ApprovalRequest, Approval: event.Approval{ID: "42", Tool: "bash", Subject: "go test ./..."}})

	loaded, ok, err := agent.LoadRuntimeMeta(sessPath)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta: err=%v ok=%v", err, ok)
	}
	if loaded.Run.Status != "waiting_approval" || loaded.Wait.ApprovalID != "42" {
		t.Fatalf("wait state not persisted: run=%q wait=%+v", loaded.Run.Status, loaded.Wait)
	}

	req, _ := http.NewRequest("POST", "/approvals/approve", strings.NewReader(`{"session_id":"approval-test","approval_id":"42"}`))
	rr := httptest.NewRecorder()
	d.handleApprove(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("approve status = %d body=%s", rr.Code, rr.Body.String())
	}
	loaded, _, _ = agent.LoadRuntimeMeta(sessPath)
	if loaded.Run.Status != "running" || loaded.Wait.Kind != "" {
		t.Fatalf("approval should clear wait state: run=%q wait=%+v", loaded.Run.Status, loaded.Wait)
	}
}

func TestDaemonRecordAskAndAnswerClearsRuntime(t *testing.T) {
	dir := t.TempDir()
	sessPath := filepath.Join(dir, "ask-test.jsonl")
	os.WriteFile(sessPath, []byte(`{"role":"user","content":"start"}`+"\n"), 0o644)
	if err := agent.SaveRuntimeMeta(sessPath, agent.RuntimeMeta{
		SessionID: "ask-test",
		Goal:      agent.RuntimeGoalMeta{Text: "needs answer", Status: control.GoalStatusRunning},
		Run:       agent.RuntimeRunMeta{Status: "running"},
	}); err != nil {
		t.Fatalf("SaveRuntimeMeta: %v", err)
	}
	d := New(Options{SessionDir: dir})
	d.scanSessions()
	ctrl := control.New(control.Options{Sink: event.Discard})
	ask := event.Ask{ID: "ask-1", Questions: []event.AskQuestion{{ID: "q1", Prompt: "Ship?"}}}
	d.mu.Lock()
	d.activeRuns["ask-test"] = &ActiveRun{
		Control:   ctrl,
		Approvals: map[string]event.Approval{},
		Asks:      map[string]event.Ask{"ask-1": ask},
	}
	d.mu.Unlock()
	d.recordWait("ask-test", agent.RuntimeWaitMeta{
		Kind:   "ask",
		Reason: "user answer required",
		AskID:  "ask-1",
		Since:  time.Now().UTC(),
	}, event.Event{Kind: event.AskRequest, Ask: ask})

	loaded, ok, err := agent.LoadRuntimeMeta(sessPath)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta: err=%v ok=%v", err, ok)
	}
	if loaded.Run.Status != "waiting_ask" || loaded.Wait.AskID != "ask-1" {
		t.Fatalf("ask wait state not persisted: run=%q wait=%+v", loaded.Run.Status, loaded.Wait)
	}

	req, _ := http.NewRequest("POST", "/asks/answer", strings.NewReader(`{"session_id":"ask-test","ask_id":"ask-1","selected":"Yes"}`))
	rr := httptest.NewRecorder()
	d.handleAnswer(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("answer status = %d body=%s", rr.Code, rr.Body.String())
	}
	loaded, _, _ = agent.LoadRuntimeMeta(sessPath)
	if loaded.Run.Status != "running" || loaded.Wait.Kind != "" {
		t.Fatalf("answer should clear wait state: run=%q wait=%+v", loaded.Run.Status, loaded.Wait)
	}
}

func TestDaemonStaleLock(t *testing.T) {
	dir := t.TempDir()
	d := New(Options{SessionDir: dir})

	lockPath := d.lockFile()
	os.MkdirAll(filepath.Dir(lockPath), 0o755)
	os.WriteFile(lockPath, []byte("99999999\n"), 0o644)

	if err := d.acquireLock(); err != nil {
		t.Fatalf("should reclaim stale lock: %v", err)
	}
	d.releaseLock()
}

func TestDaemonAuthMiddlewareRequiresToken(t *testing.T) {
	d := New(Options{SessionDir: t.TempDir(), Token: "secret-token"})
	handler := d.withAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest("GET", "/sessions", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401", rr.Code)
	}

	req = httptest.NewRequest("GET", "/sessions", nil)
	req.Header.Set("X-Reasonix-Daemon-Token", "secret-token")
	rr = httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("authorized status = %d, want 204", rr.Code)
	}
}
