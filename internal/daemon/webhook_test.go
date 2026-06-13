package daemon

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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
)

func TestWebhookValidSignature(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "webhook-test.jsonl")
	os.WriteFile(sess, []byte(`{}`), 0o644)
	agent.SaveRuntimeMeta(sess, agent.RuntimeMeta{
		SessionID: "webhook-test",
		Goal:      agent.RuntimeGoalMeta{Text: "deploy", Status: "running"},
		Run:       agent.RuntimeRunMeta{Status: "idle"},
	})

	secret := "test-secret-key"
	d := New(Options{
		SessionDir: dir,
		Webhook:    &WebhookConfig{Secret: secret, Enabled: true},
	})
	d.scanSessions()

	// Set up HTTP server.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhook", d.handleWebhook)
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

	// Build a valid webhook payload.
	payload := `{"type":"github.ci","session_id":"webhook-test","summary":"CI passed","event_id":"evt-001"}`
	sig := computeHMAC([]byte(payload), secret)

	req, _ := http.NewRequest("POST", "http://"+addr+"/webhook", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", sig)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /webhook: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Verify the session was updated.
	d.mu.RLock()
	entry := d.registry["webhook-test"]
	d.mu.RUnlock()
	if entry.Runtime.Run.Status != "pending_continue" {
		t.Errorf("Run.Status = %q, want pending_continue", entry.Runtime.Run.Status)
	}
	if entry.Runtime.Scheduler.LastWakeupEventID != "evt-001" {
		t.Errorf("LastWakeupEventID = %q, want evt-001", entry.Runtime.Scheduler.LastWakeupEventID)
	}
}

func TestWebhookInvalidSignature(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "sig-test.jsonl")
	os.WriteFile(sess, []byte(`{}`), 0o644)
	agent.SaveRuntimeMeta(sess, agent.RuntimeMeta{
		SessionID: "sig-test",
		Goal:      agent.RuntimeGoalMeta{Text: "work", Status: "running"},
		Run:       agent.RuntimeRunMeta{Status: "idle"},
	})

	d := New(Options{
		SessionDir: dir,
		Webhook:    &WebhookConfig{Secret: "correct-secret", Enabled: true},
	})
	d.scanSessions()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhook", d.handleWebhook)
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

	payload := `{"type":"test","session_id":"sig-test","summary":"bad"}`
	badSig := computeHMAC([]byte(payload), "wrong-secret")

	req, _ := http.NewRequest("POST", "http://"+addr+"/webhook", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", badSig)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /webhook: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestWebhookDedup(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "dedup-wh.jsonl")
	os.WriteFile(sess, []byte(`{}`), 0o644)
	agent.SaveRuntimeMeta(sess, agent.RuntimeMeta{
		SessionID: "dedup-wh",
		Goal:      agent.RuntimeGoalMeta{Text: "work", Status: "running"},
		Run:       agent.RuntimeRunMeta{Status: "idle"},
	})

	secret := "dedup-secret"
	d := New(Options{
		SessionDir: dir,
		Webhook:    &WebhookConfig{Secret: secret, Enabled: true},
	})
	d.scanSessions()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhook", d.handleWebhook)
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

	payload := `{"type":"github.ci","session_id":"dedup-wh","summary":"CI done","event_id":"evt-dup"}`
	sig := computeHMAC([]byte(payload), secret)

	// First request.
	req1, _ := http.NewRequest("POST", "http://"+addr+"/webhook", strings.NewReader(payload))
	req1.Header.Set("X-Webhook-Signature", sig)
	resp1, _ := client.Do(req1)
	resp1.Body.Close()

	// Second request with same event_id.
	req2, _ := http.NewRequest("POST", "http://"+addr+"/webhook", strings.NewReader(payload))
	req2.Header.Set("X-Webhook-Signature", sig)
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("second POST /webhook: %v", err)
	}
	defer resp2.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp2.Body).Decode(&result)
	if result["status"] != "duplicate" {
		t.Errorf("second request should be deduped, got status=%v", result["status"])
	}
}

func TestWebhookRoutesGitHubEventWithoutSessionID(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "github-route.jsonl")
	os.WriteFile(sess, []byte(`{}`), 0o644)
	agent.SaveRuntimeMeta(sess, agent.RuntimeMeta{
		SessionID: "github-route",
		Goal:      agent.RuntimeGoalMeta{Text: "watch esengine/DeepSeek-Reasonix PR #42", Status: "running"},
		Run:       agent.RuntimeRunMeta{Status: "idle"},
	})

	secret := "route-secret"
	d := New(Options{
		SessionDir: dir,
		Webhook:    &WebhookConfig{Secret: secret, Enabled: true},
	})
	d.scanSessions()

	payload := `{"action":"completed","repository":{"full_name":"esengine/DeepSeek-Reasonix"},"workflow_run":{"status":"completed","conclusion":"success","pull_requests":[{"number":42}],"head_branch":"feature/agentos"}}`
	sig := computeHMAC([]byte(payload), secret)
	req := httptest.NewRequest("POST", "/webhook", strings.NewReader(payload))
	req.Header.Set("X-Webhook-Signature", sig)
	req.Header.Set("X-GitHub-Event", "workflow_run")
	req.Header.Set("X-GitHub-Delivery", "delivery-42")
	rr := httptest.NewRecorder()
	d.handleWebhook(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("webhook status = %d body=%s", rr.Code, rr.Body.String())
	}
	d.mu.RLock()
	entry := d.registry["github-route"]
	d.mu.RUnlock()
	if entry.Runtime.Run.Status != "pending_continue" {
		t.Fatalf("Run.Status = %q, want pending_continue", entry.Runtime.Run.Status)
	}
	if entry.Runtime.Scheduler.LastWakeupEventID != "delivery-42" {
		t.Fatalf("LastWakeupEventID = %q, want delivery-42", entry.Runtime.Scheduler.LastWakeupEventID)
	}
}

func TestFileWatcherIgnorePatterns(t *testing.T) {
	fw := NewFileWatcher(nil, nil)

	tests := []struct {
		name    string
		pattern []string
		expect  bool
	}{
		{"node_modules", nil, true},
		{".git", nil, true},
		{"main.go", nil, false},
		{"secret.key", nil, true},
		{"backup.tmp", []string{"*.tmp"}, true},
		{"data.json", []string{"*.tmp"}, false},
	}

	for _, tt := range tests {
		got := fw.shouldIgnore(tt.name, tt.pattern)
		if got != tt.expect {
			t.Errorf("shouldIgnore(%q, %v) = %v, want %v", tt.name, tt.pattern, got, tt.expect)
		}
	}
}

func TestFileWatcherRegisterUnregister(t *testing.T) {
	fw := NewFileWatcher(nil, nil)

	cfg := FileWatchConfig{
		Paths:   []string{"/tmp/test"},
		Enabled: true,
	}
	fw.Register("sess1", cfg)

	fw.mu.Lock()
	if _, ok := fw.watches["sess1"]; !ok {
		t.Error("session not registered")
	}
	fw.mu.Unlock()

	fw.Unregister("sess1")
	fw.mu.Lock()
	if _, ok := fw.watches["sess1"]; ok {
		t.Error("session should be unregistered")
	}
	fw.mu.Unlock()
}

// --- helpers ---

func computeHMAC(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
