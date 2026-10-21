package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/base/testenv"
	"reasonix/internal/frontend/capdiag"
)

func TestDoctorCapabilitiesStaticJSON(t *testing.T) {
	root := testenv.TempDir(t)
	_ = os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# a\n"), 0o644)

	stdout, stderr := captureStd(t, func() int {
		return doctorCapabilitiesCommand([]string{"--root", root, "--json"})
	})
	if strings.TrimSpace(stderr) != "" && strings.Contains(stderr, "warning: --live") {
		t.Fatalf("static mode should not emit live warning: %s", stderr)
	}
	var report capdiag.Report
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, stdout)
	}
	if report.SchemaVersion != 1 {
		t.Fatalf("schema = %d", report.SchemaVersion)
	}
	if report.Live {
		t.Fatal("live should be false")
	}
}

func TestDoctorCapabilitiesTimeoutRequiresLive(t *testing.T) {
	rc := doctorCapabilitiesCommand([]string{"--timeout", "3s"})
	if rc != 2 {
		t.Fatalf("rc = %d, want 2", rc)
	}
}

func TestDoctorCapabilitiesTimeoutBounds(t *testing.T) {
	if rc := doctorCapabilitiesCommand([]string{"--live", "--timeout", "0.5s"}); rc != 2 {
		t.Fatalf("below min rc = %d", rc)
	}
	if rc := doctorCapabilitiesCommand([]string{"--live", "--timeout", "120s"}); rc != 2 {
		t.Fatalf("above max rc = %d", rc)
	}
}

func TestDoctorCapabilitiesUnknownArg(t *testing.T) {
	if rc := doctorCapabilitiesCommand([]string{"extra"}); rc != 2 {
		t.Fatalf("rc = %d, want 2", rc)
	}
}

func TestDoctorCapabilitiesTextOutput(t *testing.T) {
	root := testenv.TempDir(t)
	stdout, _ := captureStd(t, func() int {
		return doctorCapabilitiesCommand([]string{"--root", root})
	})
	if !strings.Contains(stdout, "Summary") || !strings.Contains(stdout, "Issues") {
		t.Fatalf("text output missing sections:\n%s", stdout)
	}
}

func captureStd(t *testing.T, fn func() int) (stdout, stderr string) {
	t.Helper()
	return captureCLIOutput(t, func() { _ = fn() })
}
