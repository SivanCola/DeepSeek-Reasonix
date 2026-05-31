package jobs

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
)

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

// A job runs to completion: Wait reports Done with its output, and the completion
// note drains exactly once.
func TestStartWaitDoneAndDrain(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()

	j := m.Start("bash", "echo", func(_ context.Context, out io.Writer) (string, error) {
		io.WriteString(out, "hello\n")
		return "", nil
	})
	res := m.Wait(context.Background(), []string{j.ID}, 5)
	if len(res) != 1 || res[0].Status != Done {
		t.Fatalf("want one Done result, got %+v", res)
	}
	if !strings.Contains(res[0].Output, "hello") {
		t.Errorf("output = %q, want it to contain hello", res[0].Output)
	}
	note := m.DrainCompletedNote()
	if !strings.Contains(note, j.ID) {
		t.Errorf("note = %q, want it to mention %s", note, j.ID)
	}
	if again := m.DrainCompletedNote(); again != "" {
		t.Errorf("second drain = %q, want empty", again)
	}
}

// Output returns only the bytes produced since the previous read.
func TestOutputStreamsIncrementally(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()

	release := make(chan struct{})
	j := m.Start("bash", "", func(_ context.Context, out io.Writer) (string, error) {
		io.WriteString(out, "first\n")
		<-release
		io.WriteString(out, "second\n")
		return "", nil
	})

	waitFor(t, func() bool {
		txt, _, _ := m.Output(j.ID)
		return strings.Contains(txt, "first")
	})
	close(release)
	m.Wait(context.Background(), []string{j.ID}, 5)

	txt, st, ok := m.Output(j.ID)
	if !ok || st != Done {
		t.Fatalf("Output after done: ok=%v status=%s", ok, st)
	}
	if !strings.Contains(txt, "second") || strings.Contains(txt, "first") {
		t.Errorf("incremental output = %q, want only the new 'second' chunk", txt)
	}
}

// Kill cancels a running job; a second Kill is a no-op once it has finished.
func TestKill(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()

	j := m.Start("bash", "", func(ctx context.Context, _ io.Writer) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	if !m.Kill(j.ID) {
		t.Fatal("Kill on a running job returned false")
	}
	res := m.Wait(context.Background(), []string{j.ID}, 5)
	if len(res) != 1 || res[0].Status != Killed {
		t.Fatalf("want Killed, got %+v", res)
	}
	if m.Kill(j.ID) {
		t.Error("Kill on a finished job should return false")
	}
}

// Close cancels every still-running job.
func TestCloseCancels(t *testing.T) {
	m := NewManager(event.Discard)

	started := make(chan struct{})
	j := m.Start("task", "", func(ctx context.Context, _ io.Writer) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	})
	<-started
	m.Close()

	res := m.Wait(context.Background(), []string{j.ID}, 5)
	if len(res) != 1 || res[0].Status != Killed {
		t.Fatalf("want Killed after Close, got %+v", res)
	}
}

// Running reflects only in-flight jobs.
func TestRunning(t *testing.T) {
	m := NewManager(event.Discard)
	defer m.Close()

	release := make(chan struct{})
	j := m.Start("task", "label", func(ctx context.Context, _ io.Writer) (string, error) {
		<-release
		return "answer", nil
	})
	waitFor(t, func() bool { return len(m.Running()) == 1 })
	if r := m.Running()[0]; r.ID != j.ID || r.Label != "label" {
		t.Errorf("running view = %+v, want id=%s label=label", r, j.ID)
	}
	close(release)
	m.Wait(context.Background(), []string{j.ID}, 5)
	waitFor(t, func() bool { return len(m.Running()) == 0 })
}
