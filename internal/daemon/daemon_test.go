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
