package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/repair"
)

func TestParseWebView2ProcessFailure(t *testing.T) {
	kind, ok := parseWebView2ProcessFailure("windows | WebVie2wProcess failed with kind 6")
	if !ok || kind != 6 {
		t.Fatalf("kind=%d ok=%v", kind, ok)
	}
	if _, ok := parseWebView2ProcessFailure("unrelated failure"); ok {
		t.Fatal("unrelated log message matched WebView2 failure")
	}
}

func TestWebView2ProcessFailureReportIsStructured(t *testing.T) {
	report := webView2ProcessFailureReport(2)
	if report.Source != "webview2.process" || report.Label != "windows.webview2.process_failed" {
		t.Fatalf("report = %+v", report)
	}
	if report.FingerprintHint != "windows.webview2.render_process_unresponsive" {
		t.Fatalf("fingerprint hint = %q", report.FingerprintHint)
	}
}

func TestPreviousRunReportUsesOnlyBoundedLifecycleContext(t *testing.T) {
	report := previousRunReport(repair.PreviousRunObservation{
		Abnormal:       true,
		Phase:          "healthy",
		Version:        "v2",
		InstallProfile: "installer",
		UpdateFrom:     "v1",
		UpdateTo:       "v2",
		UptimeBucket:   "m_2_10",
	})
	if report.Source != "native.lifecycle" || report.Label != "desktop.abnormal_exit" {
		t.Fatalf("report = %+v", report)
	}
	if !strings.Contains(report.Message, "uptime bucket: m_2_10") {
		t.Fatalf("message missing bounded uptime: %q", report.Message)
	}
}

func TestCapturePreviousFatalCrashQueuesAndRemovesRawDump(t *testing.T) {
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)
	t.Cleanup(func() { _ = os.Remove(fatalCrashPath()) })
	raw := "fatal error: concurrent map writes\n\ngoroutine 1 [running]:\nmain.run()\n\t/home/alice/project/main.go:12\n"
	if err := os.MkdirAll(filepath.Dir(fatalCrashPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fatalCrashPath(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	capturePreviousFatalCrash()
	if _, err := os.Stat(fatalCrashPath()); !os.IsNotExist(err) {
		t.Fatalf("raw fatal dump was not removed: %v", err)
	}
	report, ok := readPending(t)
	if !ok || report.Label != "go.fatal" || report.Source != "go.runtime" {
		t.Fatalf("queued report = %+v ok=%v", report, ok)
	}
	if strings.Contains(report.Stack, "/home/alice") {
		t.Fatalf("fatal stack leaked home path: %q", report.Stack)
	}
}

func TestSanitizeFatalRuntimeDumpRemovesPanicValue(t *testing.T) {
	got := sanitizeFatalRuntimeDump("panic: private prompt text\n\ngoroutine 1 [running]:\nmain.run()\n\t/home/alice/project/main.go:12\n")
	if strings.Contains(got, "private prompt text") || strings.Contains(got, "/home/alice") {
		t.Fatalf("fatal dump leaked user-controlled text: %q", got)
	}
	if !strings.Contains(got, "panic: [redacted panic value]") || !strings.Contains(got, "main.go:12") {
		t.Fatalf("fatal dump lost diagnostic structure: %q", got)
	}
}

func TestCapturePreviousFatalCrashSkipsRuntimeDuplicateOfStructuredPanic(t *testing.T) {
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)
	t.Cleanup(func() { _ = os.Remove(fatalCrashPath()) })
	if err := os.MkdirAll(filepath.Dir(fatalCrashPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fatalCrashPath(), []byte("panic: duplicate\n\ngoroutine 1 [running]:\nmain.run()\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	markFatalCrashCovered()
	capturePreviousFatalCrash()
	if _, ok := readPending(t); ok {
		t.Fatal("runtime duplicate was queued despite structured panic marker")
	}
}

func TestWindowRestoreFailureReportSeparatesTimeoutAndSource(t *testing.T) {
	report := windowRestoreFailureReport("timeout", "second_instance", "2026-07-24T08:00:00Z")
	if report.Label != "windows.window_restore.timeout" || report.TopFrame != "windows.window_restore.second_instance" {
		t.Fatalf("report = %+v", report)
	}
}
