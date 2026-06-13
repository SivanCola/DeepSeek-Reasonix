package cli

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

func TestResolveDaemonLogFile(t *testing.T) {
	dir := t.TempDir()
	if got := resolveDaemonLogFile(dir, ""); got != daemon.LogFile(dir) {
		t.Fatalf("default log path = %q, want %q", got, daemon.LogFile(dir))
	}
	custom := filepath.Join(dir, "custom.log")
	if got := resolveDaemonLogFile(dir, custom); got != custom {
		t.Fatalf("custom log path = %q, want %q", got, custom)
	}
	if got := resolveDaemonLogFile(dir, "none"); got != "" {
		t.Fatalf("disabled log path = %q, want empty", got)
	}
}

func TestNewDaemonLoggerWritesFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "nested", "daemon.log")
	var stderr bytes.Buffer
	logger, closer, err := newDaemonLogger(&stderr, logPath)
	if err != nil {
		t.Fatalf("newDaemonLogger: %v", err)
	}
	if closer == nil {
		t.Fatal("closer should be returned for file logging")
	}
	logger.Info("daemon test log", "component", "test")
	if err := closer.Close(); err != nil {
		t.Fatalf("close log: %v", err)
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile log: %v", err)
	}
	if !strings.Contains(string(b), "daemon test log") || !strings.Contains(stderr.String(), "daemon test log") {
		t.Fatalf("log should be written to file and stderr; file=%q stderr=%q", string(b), stderr.String())
	}
}

func TestNewDaemonLoggerCanDisableFile(t *testing.T) {
	logger, closer, err := newDaemonLogger(io.Discard, "")
	if err != nil {
		t.Fatalf("newDaemonLogger: %v", err)
	}
	if logger == nil || closer != nil {
		t.Fatalf("logger=%v closer=%v, want logger without closer", logger, closer)
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
		Budget: agent.RuntimeBudgetMeta{
			DailyWakeupLimit:  1,
			LastBlockedReason: "daily automatic wakeup budget exhausted for cron (1/1)",
		},
	}); err != nil {
		t.Fatalf("SaveRuntimeMeta: %v", err)
	}

	logPath := daemon.LogFile(dir)
	if err := os.WriteFile(logPath, []byte("daemon log\n"), 0o600); err != nil {
		t.Fatalf("WriteFile log: %v", err)
	}

	report, failed := buildDaemonDoctorReport("127.0.0.1:1", dir, "", func(string, string, string) (*http.Response, error) {
		return nil, errTestDaemonOffline{}
	})
	if failed {
		t.Fatalf("doctor should warn but not fail when daemon is offline: %+v", report.Checks)
	}
	if report.Runtime.Total != 1 || report.Runtime.ActiveGoals != 1 || report.Runtime.Interrupted != 1 ||
		report.Runtime.Waiting != 1 || report.Runtime.Scheduled != 1 || report.Runtime.Watched != 1 ||
		report.Runtime.Budgeted != 1 || report.Runtime.BudgetBlocked != 1 {
		t.Fatalf("unexpected runtime summary: %+v", report.Runtime)
	}
	if !hasDoctorCheck(report, "online", "warn") {
		t.Fatalf("missing online warning: %+v", report.Checks)
	}
	if report.LogFile != logPath || !hasDoctorCheck(report, "log", "ok") {
		t.Fatalf("missing log check: log=%q checks=%+v", report.LogFile, report.Checks)
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

	report, failed := buildDaemonDoctorReport("127.0.0.1:1", dir, "", nil)
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

func TestBuildDaemonDoctorReportWarnsMissingLog(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(daemon.TokenFile(dir), []byte("token\n"), 0o600); err != nil {
		t.Fatalf("WriteFile token: %v", err)
	}

	report, failed := buildDaemonDoctorReport("127.0.0.1:1", dir, "", nil)
	if failed {
		t.Fatalf("missing log file should warn, not fail: %+v", report.Checks)
	}
	if report.LogFile != daemon.LogFile(dir) || !hasDoctorCheck(report, "log", "warn") {
		t.Fatalf("missing log warning not recorded: log=%q checks=%+v", report.LogFile, report.Checks)
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
