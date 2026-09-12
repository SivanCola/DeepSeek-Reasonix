package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func cleanupDesktopTestHome(dir string) {
	// GitHub empties RUNNER_TEMP after each job. Synchronous Windows cleanup of
	// this large test state has taken 3-10 minutes after PASS. Only defer cleanup
	// when the directory belongs to that runner; local paths still clean here.
	if runtime.GOOS == "windows" && os.Getenv("GITHUB_ACTIONS") == "true" {
		runnerTemp := os.Getenv("RUNNER_TEMP")
		if relative, err := filepath.Rel(runnerTemp, dir); runnerTemp != "" && err == nil &&
			relative != "." && relative != ".." && !filepath.IsAbs(relative) &&
			!strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return
		}
	}
	_ = os.RemoveAll(dir)
}

// TestLifecycleDiagnosticsUsePreShellOwnershipGate pins the ordering that keeps
// a superseded process from consuming lifecycle evidence: the host RPC service
// claims diagnostics ownership before it serves its first request.
func TestLifecycleDiagnosticsUsePreShellOwnershipGate(t *testing.T) {
	source, err := os.ReadFile("host_rpc.go")
	if err != nil {
		t.Fatal(err)
	}
	beforeServe, _, ok := strings.Cut(string(source), "server.Serve(appCtx)")
	if !ok {
		t.Fatal("host_rpc.go no longer contains the host RPC serve boundary")
	}
	if !strings.Contains(beforeServe, "prepareDesktopDiagnostics(app)") {
		t.Fatal("host service must claim diagnostics ownership before serving")
	}

	appSource, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	_, afterStartup, ok := strings.Cut(string(appSource), "func (a *App) startup(ctx context.Context) {")
	if !ok {
		t.Fatal("app.go no longer contains App.startup")
	}
	startupBody, _, ok := strings.Cut(afterStartup, "\n}")
	if !ok || !strings.Contains(startupBody, "initializeLifecycleDiagnostics(a)") {
		t.Fatal("previous lifecycle consumption must remain owned by startup")
	}
}

// TestMain isolates user config/state/cache dirs for the whole package. Without
// this, tests that persist desktop state, sessions, cache, or CLI-style config
// can leak into the developer's real Reasonix directories.
func TestMain(m *testing.M) {
	tempRoot := ""
	if runtime.GOOS == "windows" && os.Getenv("GITHUB_ACTIONS") == "true" {
		tempRoot = os.Getenv("RUNNER_TEMP")
	}
	dir, err := os.MkdirTemp(tempRoot, "reasonix-desktop-test")
	if err != nil {
		os.Exit(1)
	}
	os.Setenv("HOME", dir)
	os.Setenv("REASONIX_CREDENTIALS_STORE", "file")
	os.Setenv("USERPROFILE", dir)
	os.Setenv("XDG_CONFIG_HOME", dir+"/config")
	os.Setenv("REASONIX_STATE_HOME", dir+"/state")
	os.Setenv("REASONIX_CACHE_HOME", dir+"/cache")
	os.Setenv("AppData", dir)
	// Tests fail closed for telemetry. Any test that expects a request must
	// replace the relevant endpoint with an httptest.Server explicitly.
	crashEndpoint = "http://127.0.0.1:0/v1/report"
	pingEndpoint = "http://127.0.0.1:0/v1/ping"
	metricsEndpoint = "http://127.0.0.1:0/v1/metrics"
	// Neutralize the host event bridge because there is no shell here. Tests
	// asserting runtime events install a higher-priority per-instance capture.
	runtimeEventsEmitFallback = func(context.Context, string, ...any) {}
	code := m.Run()
	cleanupDesktopTestHome(dir)
	os.Exit(code)
}

func TestDesktopTestTelemetryEndpointsAreFailClosed(t *testing.T) {
	for name, endpoint := range map[string]string{
		"crash":   crashEndpoint,
		"ping":    pingEndpoint,
		"metrics": metricsEndpoint,
	} {
		if strings.Contains(endpoint, "crash.reasonix.io") {
			t.Fatalf("%s test endpoint targets production: %s", name, endpoint)
		}
	}
}
