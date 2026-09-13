package control

import (
	"errors"
	"os"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/sessionv3"
	"reasonix/internal/tool"
)

func TestGoalCommandDoesNotStartProviderAfterPersistenceFailure(t *testing.T) {
	store, err := sessionv3.CreateWithOptions(t.TempDir()+"/goal-command-failure", "goal-command-failure", sessionv3.OpenOptions{
		Sync: func(*os.File) error { return errors.New("injected sync failure") },
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := sessionv3.NewService("desktop", failingFlushPersistence{session: store})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := service.Create(t.Context(), sessionv3.CreateOptions{SessionID: "goal-command-failure"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().AppendBatch(t.Context(), "seed", []sessionv3.Event{{Kind: "tool/result", Payload: []byte(`{"id":"seed-call","name":"bash","output":"seed"}`)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().Flush(t.Context()); err == nil {
		t.Fatal("seed Flush unexpectedly succeeded")
	}
	runner := &modelErrorGoalRunner{}
	exec := agent.New(nil, tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
	c := New(Options{Runner: runner, Executor: exec, Sink: event.Discard, SessionService: service, SessionRuntime: runtime, ExclusiveSessionV3: true})
	t.Cleanup(c.ReleaseResources)

	c.Submit("/goal ship the durable fix")
	if runner.calls != 0 {
		t.Fatalf("provider calls = %d, want zero", runner.calls)
	}
	if snapshot := runtime.Session().Snapshot(); snapshot.PersistenceStatus != sessionv3.PersistenceUncertain {
		t.Fatalf("persistence status = %q, want uncertain", snapshot.PersistenceStatus)
	}
}
