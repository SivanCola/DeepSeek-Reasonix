package builtin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/jobs"
	"reasonix/internal/resourcebudget"
	"reasonix/internal/tool"
)

func TestBashResourceAdmissionBeforeForegroundLaunch(t *testing.T) {
	requirePOSIXShellTest(t)
	for _, persistent := range []bool{false, true} {
		t.Run(map[bool]string{false: "one-shot", true: "persistent"}[persistent], func(t *testing.T) {
			ctx := resourcebudget.WithDirectory(fullAccessBashTestContext(t.Context()), t.TempDir())
			lease, err := resourcebudget.Acquire(ctx, "pnpm test", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			dir := t.TempDir()
			b := bash{workDir: dir}
			if persistent {
				b = persistentBash(t, dir)
			}
			waiting := make(chan struct{})
			var once sync.Once
			queued, cancel := context.WithCancel(tool.WithProgress(ctx, func(text string) {
				if strings.Contains(text, "resource budget") {
					once.Do(func() { close(waiting) })
				}
			}))
			defer cancel()
			args := argsJSON(t, map[string]any{"command": "sh -c 'printf ran > marker'"})
			done := make(chan error, 1)
			go func() { _, err := b.Execute(queued, args); done <- err }()
			select {
			case <-waiting:
			case <-time.After(5 * time.Second):
				t.Fatal("command never queued")
			}
			if _, err := os.Stat(filepath.Join(dir, "marker")); !os.IsNotExist(err) {
				t.Fatal("queued command launched")
			}
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("queued cancellation: %v", err)
			}
			lease.Release()
			if _, err := b.Execute(ctx, args); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, "marker")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBashBackgroundResourceWaitCanBeKilledWithoutLaunching(t *testing.T) {
	requirePOSIXShellTest(t)
	ctx := resourcebudget.WithDirectory(fullAccessBashTestContext(t.Context()), t.TempDir())
	lease, err := resourcebudget.Acquire(ctx, "pnpm test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	m := jobs.NewManager(event.Discard)
	defer m.Close()
	ctx = jobs.WithSession(jobs.WithManager(ctx, m), "resource-session")
	dir := t.TempDir()
	b := bash{workDir: dir}
	if _, err := b.Execute(ctx, argsJSON(t, map[string]any{"command": "sh -c 'printf ran > marker'", "run_in_background": true})); err != nil {
		t.Fatal(err)
	}
	running := m.RunningForSession("resource-session")
	if len(running) != 1 {
		t.Fatalf("queued job was not registered: %v", running)
	}
	id := running[0].ID
	deadline := time.After(5 * time.Second)
	for {
		text, _, _ := m.OutputForSession("resource-session", id)
		if strings.Contains(text, "resource budget") {
			break
		}
		select {
		case <-deadline:
			t.Fatal("job never queued")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !m.KillForSession("resource-session", id) {
		t.Fatal("queued kill failed")
	}
	results := m.WaitForSession(ctx, "resource-session", []string{id}, 5)
	if len(results) != 1 || results[0].Status != jobs.Killed {
		t.Fatalf("queued result: %v", results)
	}
	lease.Release()
	if _, err := os.Stat(filepath.Join(dir, "marker")); !os.IsNotExist(err) {
		t.Fatal("killed queued job launched")
	}
}

func TestBashResourceBudgetPreservesPersistentWorkerExports(t *testing.T) {
	requirePOSIXShellTest(t)
	ctx := resourcebudget.WithDirectory(fullAccessBashTestContext(t.Context()), t.TempDir())
	b := persistentBash(t, t.TempDir())
	if _, err := b.Execute(ctx, argsJSON(t, map[string]any{"command": "export GOMAXPROCS=1; export RESOURCE_TEST_VALUE=kept"})); err != nil {
		t.Fatal(err)
	}
	out, err := b.Execute(ctx, argsJSON(t, map[string]any{"command": `printf '%s %s' "$GOMAXPROCS" "$RESOURCE_TEST_VALUE"`}))
	if err != nil || strings.TrimSpace(out) != "1 kept" {
		t.Fatalf("persistent worker exports were reset: %q %v", out, err)
	}
}

func TestBashPersistentTaskReservationClampsWorkersWithoutReset(t *testing.T) {
	requirePOSIXShellTest(t)
	ctx := resourcebudget.WithDirectory(fullAccessBashTestContext(t.Context()), t.TempDir())
	b := persistentBash(t, t.TempDir())
	if _, err := b.Execute(ctx, argsJSON(t, map[string]any{"command": "export RESOURCE_TEST_VALUE=kept; export VITEST_MAX_WORKERS=100"})); err != nil {
		t.Fatal(err)
	}
	ctx = resourcebudget.WithRequest(ctx, resourcebudget.Resources{CPU: 1})
	out, err := b.Execute(ctx, argsJSON(t, map[string]any{"command": `sh -c 'printf "%s %s %s" "$VITEST_MAX_WORKERS" "$GOMAXPROCS" "$RESOURCE_TEST_VALUE"'`}))
	if err != nil || strings.TrimSpace(out) != "1 1 kept" {
		t.Fatalf("reservation/exports: %q %v", out, err)
	}
}
