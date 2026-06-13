package daemon

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
)

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
