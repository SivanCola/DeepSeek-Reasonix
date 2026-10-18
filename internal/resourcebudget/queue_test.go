package resourcebudget

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/filelock"
)

func TestQueuedProcessCrashPreservesCrossProcessFIFO(t *testing.T) {
	if dir := os.Getenv("REASONIX_QUEUED_CHILD"); dir != "" {
		ctx := WithDirectory(context.Background(), dir)
		ctx = WithWaitNotice(ctx, func() { _, _ = os.Stdout.WriteString("queued\n") })
		l, err := Acquire(ctx, "go test", nil)
		if l != nil {
			l.Release()
		}
		if err != nil {
			os.Exit(2)
		}
		os.Exit(3) // Parent must kill this queued process before admission.
	}
	ctx := configuredContext(t, Resources{1, 100}, Resources{1, 40})
	a, err := Acquire(ctx, "go test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	dir, _ := directory(ctx)
	child := exec.Command(os.Args[0], "-test.run=^TestQueuedProcessCrashPreservesCrossProcessFIFO$")
	child.Dir = t.TempDir() // Independent worktree/cwd must share the same queue.
	child.Env = append(os.Environ(), "REASONIX_QUEUED_CHILD="+dir)
	out, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	kill := func() { once.Do(func() { _ = child.Process.Kill(); _ = child.Wait() }) }
	t.Cleanup(kill)
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(out).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if line != "queued\n" {
			t.Fatalf("child: %q", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("child did not register")
	}
	c := startPending(t, ctx, Resources{})
	if w := waitPending(t, c); w.Position != 2 {
		t.Fatalf("cross-process FIFO: %+v", w)
	}
	kill()
	select {
	case w := <-c.waits:
		if w.Position != 1 {
			t.Fatalf("dead head retained: %+v", w)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dead queued owner blocked follower")
	}
	a.Release()
	admittedPending(t, c)
}

func TestInheritedLeaseKeepsReservedCPUWithoutAdditionalAdmission(t *testing.T) {
	ctx := configuredContext(t, Resources{1, 100}, Resources{1, 40})
	l, err := Acquire(ctx, "go test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	nested, err := Acquire(WithRequest(ctx, Resources{99, 999}), "go test", l.Env(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer nested.Release()
	if nested.cores != 1 || value(nested.Env(nil), "VITEST_MAX_WORKERS") != "1" {
		t.Fatal("nested task replaced the parent's reservation")
	}
	s, err := Status(ctx)
	if err != nil || s.Running != 1 {
		t.Fatalf("nested counted twice: %+v %v", s, err)
	}
}

func configuredContext(t *testing.T, budget, task Resources) context.Context {
	t.Helper()
	ctx := WithDirectory(t.Context(), t.TempDir())
	if err := Configure(ctx, Config{Budget: budget, Task: task}); err != nil {
		t.Fatal(err)
	}
	return ctx
}

type pending struct {
	waits  chan WaitInfo
	done   chan *Lease
	errors chan error
	cancel context.CancelFunc
}

func startPending(t *testing.T, ctx context.Context, request Resources) pending {
	t.Helper()
	p := pending{waits: make(chan WaitInfo, 32), done: make(chan *Lease, 1), errors: make(chan error, 1)}
	ctx, p.cancel = context.WithCancel(WithRequest(ctx, request))
	t.Cleanup(p.cancel)
	ctx = WithWaitDetails(ctx, func(w WaitInfo) { p.waits <- w })
	finished := make(chan struct{})
	t.Cleanup(func() {
		p.cancel()
		<-finished
		select {
		case l := <-p.done:
			l.Release()
		default:
		}
	})
	go func() {
		defer close(finished)
		lease, err := Acquire(ctx, "go test ./...", nil)
		if err != nil {
			p.errors <- err
			return
		}
		p.done <- lease
	}()
	return p
}

func waitPending(t *testing.T, p pending) WaitInfo {
	t.Helper()
	select {
	case w := <-p.waits:
		return w
	case l := <-p.done:
		l.Release()
		t.Fatal("task bypassed queue")
	case err := <-p.errors:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("no wait reason")
	}
	return WaitInfo{}
}

func admittedPending(t *testing.T, p pending) *Lease {
	t.Helper()
	select {
	case l := <-p.done:
		t.Cleanup(l.Release)
		return l
	case err := <-p.errors:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("task not admitted")
	}
	return nil
}

func TestMemoryAdmissionFIFOAndNoBackfill(t *testing.T) {
	// All tasks fit the CPU budget; memory alone blocks B. C fits the spare
	// memory but must not overtake B. Queue registration is synchronized by
	// wait callbacks, rather than assumptions about goroutine timing.
	ctx := configuredContext(t, Resources{1, 100}, Resources{1, 60})
	// Use the host-independent single CPU budget with a CPU reservation test
	// below; memory/FIFO admission is exercised with two CPU slots when present.
	if runtime.NumCPU() < 2 {
		t.Skip("requires two CPU slots")
	}
	if err := Configure(ctx, Config{Resources{2, 100}, Resources{1, 60}}); err != nil {
		t.Fatal(err)
	}
	a, err := Acquire(ctx, "pnpm test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	b := startPending(t, ctx, Resources{1, 60})
	w := waitPending(t, b)
	if w.Position != 1 || w.Reason != "memory budget in use" || w.Used.MemoryMiB != 60 {
		t.Fatalf("memory wait: %+v", w)
	}
	c := startPending(t, ctx, Resources{1, 40})
	w = waitPending(t, c)
	if w.Position != 2 || !strings.Contains(w.Reason, "FIFO") {
		t.Fatalf("FIFO wait: %+v", w)
	}
	a.Release()
	bl := admittedPending(t, b)
	defer bl.Release()
	cl := admittedPending(t, c)
	defer cl.Release()
	s, err := Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.Running != 2 || s.Reserved != (Resources{2, 100}) || s.Waiting != 0 {
		t.Fatalf("reservations: %+v", s)
	}
}

func TestCPUAdmissionAndCancelledHeadUnblocksFollower(t *testing.T) {
	ctx := configuredContext(t, Resources{1, 100}, Resources{1, 10})
	a, err := Acquire(ctx, "pnpm test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	b := startPending(t, ctx, Resources{1, 10})
	if w := waitPending(t, b); w.Reason != "CPU budget in use" {
		t.Fatalf("CPU wait: %+v", w)
	}
	c := startPending(t, ctx, Resources{1, 10})
	if w := waitPending(t, c); w.Position != 2 {
		t.Fatalf("queue wait: %+v", w)
	}
	b.cancel()
	if err := <-b.errors; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	select {
	case w := <-c.waits:
		if w.Position != 1 || w.Reason != "CPU budget in use" {
			t.Fatalf("updated wait: %+v", w)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait reason did not update")
	}
	a.Release()
	admittedPending(t, c)
}

func TestWaitCallbackCanReadStatusWithoutHoldingSchedulerLock(t *testing.T) {
	ctx := configuredContext(t, Resources{1, 100}, Resources{1, 40})
	l, err := Acquire(ctx, "go test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var statusErr error
	waitCtx = WithWaitDetails(waitCtx, func(w WaitInfo) {
		_, statusErr = Status(waitCtx)
		cancel()
	})
	_, err = Acquire(waitCtx, "go test", nil)
	if !errors.Is(err, context.Canceled) || statusErr != nil {
		t.Fatalf("progress callback held scheduler lock: %v %v", err, statusErr)
	}
}

func TestSharedConfigurationChangesDoNotPreemptOwners(t *testing.T) {
	ctx := configuredContext(t, Resources{1, 100}, Resources{1, 60})
	a, err := Acquire(ctx, "go test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	b := startPending(t, ctx, Resources{1, 40})
	waitPending(t, b)
	if err := Configure(ctx, Config{Resources{1, 50}, Resources{1, 40}}); err != nil {
		t.Fatal(err)
	}
	s, err := Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.Reserved.MemoryMiB != 60 || s.Running != 1 {
		t.Fatalf("live owner changed: %+v", s)
	}
	a.Release()
	admittedPending(t, b)
	if err := Configure(ctx, Config{Resources{1, 50}, Resources{1, 51}}); err == nil {
		t.Fatal("oversized default accepted")
	}
	if _, err := Acquire(WithRequest(ctx, Resources{1, 51}), "go test", nil); err == nil {
		t.Fatal("oversized request queued forever")
	}
	if _, err := Acquire(ctx, "go test", []string{"REASONIX_TASK_CPU=bad"}); err == nil {
		t.Fatal("invalid hint accepted")
	}
}

func TestLivePolicyShrinkRejectsQueuedOversizedRequest(t *testing.T) {
	ctx := configuredContext(t, Resources{1, 100}, Resources{1, 60})
	a, err := Acquire(ctx, "go test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	b := startPending(t, ctx, Resources{1, 60})
	waitPending(t, b)
	if err := Configure(ctx, Config{Resources{1, 50}, Resources{1, 40}}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-b.errors:
		if !strings.Contains(err.Error(), "fit budget") {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("impossible queued request did not fail")
	}
}

func TestQueueCrashArtifactsAndLegacyCompatibility(t *testing.T) {
	ctx := configuredContext(t, Resources{1, 100}, Resources{1, 40})
	dir, _ := directory(ctx)
	if err := os.MkdirAll(filepath.Join(dir, "queue-v2"), 0700); err != nil {
		t.Fatal(err)
	}
	dead := strings.Repeat("a", 32)
	orphan := filepath.Join(dir, strings.Repeat("c", 32)+".lease")
	if err := os.WriteFile(orphan, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entryPath(dir, dead), []byte("crash interrupted metadata"), 0600); err != nil {
		t.Fatal(err)
	}
	// A dead owner is reclaimable even if its metadata was incomplete.
	s, err := Status(ctx)
	if err != nil || s.Waiting != 0 {
		t.Fatalf("dead entry: %+v %v", s, err)
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unpublished crash lease was not reclaimed: %v", err)
	}
	legacy, err := filelock.TryAcquire(filepath.Join(dir, "heavy.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer legacy()
	p := startPending(t, ctx, Resources{})
	if w := waitPending(t, p); !strings.Contains(w.Reason, "legacy") {
		t.Fatalf("legacy wait: %+v", w)
	}
	legacy()
	l := admittedPending(t, p)
	defer l.Release()
	if unlock, err := filelock.TryAcquire(filepath.Join(dir, "heavy.lock")); err == nil {
		unlock()
		t.Fatal("v1 task overlapped v2 owner")
	} else if !errors.Is(err, filelock.ErrHeld) {
		t.Fatal(err)
	}
}

func TestUnknownPolicyAndLiveQueueFormatsFailClosed(t *testing.T) {
	ctx := configuredContext(t, Resources{1, 100}, Resources{1, 40})
	dir, _ := directory(ctx)
	if err := os.WriteFile(configPath(dir), []byte(`{"future":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(ctx, "go test", nil); err == nil {
		t.Fatal("unknown policy ignored")
	}
	if err := Configure(ctx, Config{Resources{1, 100}, Resources{1, 40}}); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("b", 32)
	unlock, err := filelock.TryAcquire(filepath.Join(dir, token+".lease"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := writeJSON(entryPath(dir, token), entry{Version: 3, Ticket: 1, Request: Resources{1, 40}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Status(ctx); err == nil {
		t.Fatal("live future queue format silently ignored")
	}
}
