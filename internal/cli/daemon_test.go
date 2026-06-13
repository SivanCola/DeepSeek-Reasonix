package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/daemon"
)

func TestResolveDaemonWebhookConfigDisabled(t *testing.T) {
	cfg, err := resolveDaemonWebhookConfig(false, "", func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolveDaemonWebhookConfig: %v", err)
	}
	if cfg != nil {
		t.Fatalf("config = %+v, want nil", cfg)
	}
}

func TestResolveDaemonWebhookConfigRequiresSecret(t *testing.T) {
	_, err := resolveDaemonWebhookConfig(true, "", func(string) string { return "" })
	if err == nil {
		t.Fatal("expected missing secret error")
	}
}

func TestResolveDaemonWebhookConfigFromFlag(t *testing.T) {
	cfg, err := resolveDaemonWebhookConfig(true, "  flag-secret  ", func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolveDaemonWebhookConfig: %v", err)
	}
	if cfg == nil || !cfg.Enabled || cfg.Secret != "flag-secret" {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestResolveDaemonWebhookConfigFromEnv(t *testing.T) {
	cfg, err := resolveDaemonWebhookConfig(false, "", func(key string) string {
		if key != "REASONIX_DAEMON_WEBHOOK_SECRET" {
			t.Fatalf("unexpected env key %q", key)
		}
		return "env-secret"
	})
	if err != nil {
		t.Fatalf("resolveDaemonWebhookConfig: %v", err)
	}
	if cfg == nil || !cfg.Enabled || cfg.Secret != "env-secret" {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestBuildDaemonDoctorReportSummarizesRuntime(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(daemon.TokenFile(dir), []byte("token\n"), 0o600); err != nil {
		t.Fatalf("WriteFile token: %v", err)
	}
	sessionPath := filepath.Join(dir, "agentos.jsonl")
	if err := os.WriteFile(sessionPath, []byte(`{"role":"user","content":"start"}`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile session: %v", err)
	}
	if err := agent.SaveRuntimeMeta(sessionPath, agent.RuntimeMeta{
		SessionID: "agentos",
		Goal:      agent.RuntimeGoalMeta{Text: "ship daemon", Status: "running"},
		Run:       agent.RuntimeRunMeta{Status: "interrupted"},
		Wait:      agent.RuntimeWaitMeta{Kind: "event", EventSource: "github"},
		Scheduler: agent.RuntimeSchedMeta{Enabled: true, Interval: time.Hour},
		FileWatch: agent.RuntimeWatchMeta{Enabled: true, Paths: []string{"src"}},
	}); err != nil {
		t.Fatalf("SaveRuntimeMeta: %v", err)
	}

	report, failed := buildDaemonDoctorReport("127.0.0.1:1", dir, func(string, string, string) (*http.Response, error) {
		return nil, errTestDaemonOffline{}
	})
	if failed {
		t.Fatalf("doctor should warn but not fail when daemon is offline: %+v", report.Checks)
	}
	if report.Runtime.Total != 1 || report.Runtime.ActiveGoals != 1 || report.Runtime.Interrupted != 1 ||
		report.Runtime.Waiting != 1 || report.Runtime.Scheduled != 1 || report.Runtime.Watched != 1 {
		t.Fatalf("unexpected runtime summary: %+v", report.Runtime)
	}
	if !hasDoctorCheck(report, "online", "warn") {
		t.Fatalf("missing online warning: %+v", report.Checks)
	}
}

func TestBuildDaemonDoctorReportFailsCorruptRuntime(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(daemon.TokenFile(dir), []byte("token\n"), 0o600); err != nil {
		t.Fatalf("WriteFile token: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.jsonl.runtime.json"), []byte("{not-json"), 0o644); err != nil {
		t.Fatalf("WriteFile runtime: %v", err)
	}

	report, failed := buildDaemonDoctorReport("127.0.0.1:1", dir, nil)
	if !failed {
		t.Fatalf("doctor should fail for corrupt runtime: %+v", report.Checks)
	}
	if report.Runtime.Corrupt != 1 {
		t.Fatalf("Corrupt = %d, want 1", report.Runtime.Corrupt)
	}
	if !hasDoctorCheck(report, "runtime", "fail") {
		t.Fatalf("missing runtime failure: %+v", report.Checks)
	}
}

type errTestDaemonOffline struct{}

func (errTestDaemonOffline) Error() string { return "offline" }

func hasDoctorCheck(report daemonDoctorReport, name, status string) bool {
	for _, check := range report.Checks {
		if check.Name == name && check.Status == status {
			return true
		}
	}
	return false
}
