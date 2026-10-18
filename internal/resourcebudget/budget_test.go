package resourcebudget

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAdmissionAcrossSessionsAndWorktrees(t *testing.T) {
	ctx := WithDirectory(t.Context(), t.TempDir())
	first, err := Acquire(ctx, "pnpm test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	assertQueuedAndCancel(t, ctx, nil)
	// Reading diagnostics must remain possible while another chat tests.
	light, err := Acquire(ctx, "git status --short", nil)
	if err != nil {
		t.Fatal(err)
	}
	light.Release()
	first.Release()
	second, err := Acquire(ctx, "go test ./...", nil)
	if err != nil {
		t.Fatal(err)
	}
	second.Release()
}

func TestNestedAdmissionRequiresLiveLease(t *testing.T) {
	ctx := WithDirectory(t.Context(), t.TempDir())
	lease, err := Acquire(ctx, "pnpm test", nil)
	if err != nil {
		t.Fatal(err)
	}
	env := lease.Env(nil)
	nested, err := Acquire(ctx, "node custom-test.mjs", env)
	if err != nil {
		t.Fatal(err)
	}
	nested.Release()
	lease.Release()
	other, err := Acquire(ctx, "go test ./...", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()
	assertQueuedAndCancel(t, ctx, env)
}

func TestHeavyClassification(t *testing.T) {
	for _, command := range []string{"go test ./...", "pnpm test", "npm run check", "python custom.py", "bash -c 'go test ./...'", "git status; pnpm test", "echo $(go test ./...)", "git diff --ext-diff", "git -c core.fsmonitor=evil status"} {
		if !Heavy(command) {
			t.Errorf("unbounded command classified light: %s", command)
		}
	}
	for _, command := range []string{"git status --short", "git diff --stat", "pwd", "rg -n needle src", "echo done"} {
		if Heavy(command) {
			t.Errorf("diagnostic command classified heavy: %s", command)
		}
	}
	for _, command := range []string{"pnpm dev", "npm.cmd run dev", "npm run start", "pnpm --dir frontend dev", "vite --host 127.0.0.1", "cd frontend && pnpm dev", "sleep 1; git status --short"} {
		if Heavy(command) {
			t.Errorf("service dependency must not monopolize batch lane: %s", command)
		}
	}
	if got := goPackageLimit(`-ldflags='-X example.value=hello -p=1'`); got != 0 {
		t.Fatalf("linker argument mistaken for global parallelism: %d", got)
	}
}

func TestWorkerEnvironmentPreservesSmallerLimits(t *testing.T) {
	l := &Lease{cores: 4}
	env := l.Env([]string{"KEEP=value", "GOMAXPROCS=2", "CARGO_BUILD_JOBS=32", "GOFLAGS=-tags=example -p=1"})
	joined := strings.Join(env, "\n")
	for _, want := range []string{"KEEP=value", "GOMAXPROCS=2", "CARGO_BUILD_JOBS=4", "GOFLAGS=-tags=example -p=1", "CMAKE_BUILD_PARALLEL_LEVEL=4"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %s", want, joined)
		}
	}
	for _, flags := range []string{"", "-p=32", "-p=32 -p=1"} {
		env := l.Env([]string{"GOFLAGS=" + flags})
		jobs := 2
		if flags == "-p=32 -p=1" {
			jobs = 1
		}
		if value(env, "GOMAXPROCS") != strconv.Itoa(4/jobs) {
			t.Fatalf("Go package × process parallelism exceeds budget: %v", env)
		}
		if value(env, "VITEST_MAX_WORKERS") != "4" {
			t.Fatal("Vitest worker cap missing")
		}
	}
}

func TestPersistentCommandClampsPercentagesAndDoesNotGrowGOFLAGS(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("POSIX shell unavailable")
	}
	l := &Lease{cores: 1}
	command := l.Command("true") + "; " + l.Command(`printf '%s|%s|%s|%s|%s' "$VITEST_MAX_WORKERS" "$GOMAXPROCS" "$GOFLAGS" "$VITEST_MIN_THREADS" "$VITEST_MIN_FORKS"`)
	for _, percentage := range []string{"0.5%", "08%", "100%"} {
		cmd := exec.Command("sh", "-c", command)
		cmd.Env = append(os.Environ(), "VITEST_MAX_WORKERS="+percentage, "GOMAXPROCS=1", "GOFLAGS=-tags=example", "VITEST_MIN_THREADS=8", "VITEST_MIN_FORKS=8")
		out, err := cmd.CombinedOutput()
		if err != nil || string(out) != "1|1|-tags=example -p=1|1|1" {
			t.Fatalf("persistent %s: %s %v", percentage, out, err)
		}
	}
}

func TestProcessCrashReleasesAdmission(t *testing.T) {
	if dir := os.Getenv("REASONIX_BUDGET_TEST_CHILD"); dir != "" {
		lease, err := Acquire(WithDirectory(context.Background(), dir), "go test ./...", nil)
		if err != nil {
			os.Exit(2)
		}
		defer lease.Release()
		if err := os.WriteFile(filepath.Join(dir, "ready"), nil, 0600); err != nil {
			os.Exit(3)
		}
		time.Sleep(time.Hour)
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessCrashReleasesAdmission$")
	cmd.Env = append(os.Environ(), "REASONIX_BUDGET_TEST_CHILD="+dir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.After(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("helper did not acquire budget")
		case <-time.After(10 * time.Millisecond):
		}
	}
	ctx := WithDirectory(t.Context(), dir)
	assertQueuedAndCancel(t, ctx, nil)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	ctx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	lease, err := Acquire(ctx, "pnpm test", nil)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
}

func TestInheritedScopeSurvivesPrivateCacheEnvironment(t *testing.T) {
	if os.Getenv("REASONIX_PRIVATE_CACHE_CHILD") == "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		lease, err := Acquire(ctx, "go test ./...", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer lease.Release()
		if lease.token != os.Getenv(ownerEnv) {
			t.Fatal("private caches replaced the inherited resource owner")
		}
		return
	}
	dir := t.TempDir()
	lease, err := Acquire(WithDirectory(t.Context(), dir), "go test ./...", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	privateHome := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestInheritedScopeSurvivesPrivateCacheEnvironment$")
	cmd.Env = append(lease.Env(os.Environ()), "REASONIX_PRIVATE_CACHE_CHILD=1", "HOME="+privateHome, "USERPROFILE="+privateHome, "XDG_CACHE_HOME="+filepath.Join(privateHome, "private-cache"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child did not reuse the host scope: %v\n%s", err, out)
	}
}

func assertQueuedAndCancel(t *testing.T, ctx context.Context, env []string) {
	t.Helper()
	waiting := make(chan struct{})
	ctx, cancel := context.WithCancel(WithWaitNotice(ctx, func() { close(waiting) }))
	defer cancel()
	done := make(chan error, 1)
	go func() {
		lease, err := Acquire(ctx, "go test ./...", env)
		if lease != nil {
			lease.Release()
		}
		done <- err
	}()
	select {
	case <-waiting:
	case err := <-done:
		t.Fatalf("heavy task bypassed a live owner: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("admission never reported a wait")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation: %v", err)
	}
}
