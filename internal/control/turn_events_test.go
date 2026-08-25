package control

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/event"
)

type turnEventGateRunner struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (r *turnEventGateRunner) Run(context.Context, string) error {
	r.calls.Add(1)
	close(r.started)
	<-r.release
	return nil
}

func TestTurnAdmissionIsDurableBeforeRunnerStarts(t *testing.T) {
	dir := t.TempDir()
	runner := &turnEventGateRunner{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan event.Event, 1)
	c := New(Options{
		Runner: runner,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.TurnDone {
				done <- e
			}
		}),
		SessionDir: dir, SessionPath: filepath.Join(dir, "session.jsonl"),
	})
	t.Cleanup(c.Close)

	c.Submit("run")
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	records, err := c.TurnEventsAfter(0)
	if err != nil {
		t.Fatalf("TurnEventsAfter: %v", err)
	}
	if len(records) < 2 || records[0].Status != event.TurnQueued || records[1].Kind != "turn_started" || records[1].Status != event.TurnInProgress {
		t.Fatalf("admission prefix = %+v, want queued then durable in_progress start", records)
	}
	close(runner.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish")
	}
	records, err = c.TurnEventsAfter(0)
	if err != nil {
		t.Fatalf("TurnEventsAfter terminal: %v", err)
	}
	started := 0
	for _, record := range records {
		if record.Kind == "turn_started" {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("turn_started records = %d, want exactly one", started)
	}
}

func TestTurnAdmissionLedgerFailureDoesNotRunProvider(t *testing.T) {
	dir := t.TempDir()
	blockedParent := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blockedParent, []byte("block"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	runner := &turnEventGateRunner{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan event.Event, 1)
	c := New(Options{
		Runner: runner,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.TurnDone {
				done <- e
			}
		}),
		SessionDir: dir, SessionPath: filepath.Join(blockedParent, "session.jsonl"),
	})
	t.Cleanup(c.Close)

	c.Submit("must not reach provider")
	select {
	case terminal := <-done:
		if terminal.Err == nil || !strings.Contains(terminal.Err.Error(), "persist turn admission") {
			t.Fatalf("terminal error = %v, want explicit ledger admission failure", terminal.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("failed admission did not terminate")
	}
	if got := runner.calls.Load(); got != 0 {
		t.Fatalf("runner calls = %d, want provider side effects blocked", got)
	}
}
