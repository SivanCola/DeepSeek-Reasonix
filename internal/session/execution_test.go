package session

import (
	"context"
	"sync"
	"testing"
)

type testExecution struct {
	mu      sync.Mutex
	phase   RuntimePhase
	cancel  context.CancelFunc
	ctx     context.Context
	gen     uint64
	runtime *Runtime
}

func bindTestExecution(t *testing.T, runtime *Runtime, name string) (context.Context, *testExecution) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	exec := &testExecution{phase: RuntimeRunning, cancel: cancel, ctx: ctx, gen: 1, runtime: runtime}
	exec.gen = runtime.BindExecution(exec)
	runtime.NoteExecution(exec.gen, RuntimeRunning, name)
	t.Cleanup(exec.Finish)
	return ctx, exec
}

func (e *testExecution) Snapshot() RuntimeSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return RuntimeSnapshot{Phase: e.phase}
}

func (e *testExecution) Cancel() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.phase != RuntimeRunning && e.phase != RuntimeCancelling {
		return false
	}
	e.phase = RuntimeCancelling
	if e.cancel != nil {
		e.cancel()
	}
	return true
}

func (e *testExecution) Finish() {
	e.mu.Lock()
	if e.phase == RuntimeIdle || e.phase == RuntimeClosed {
		e.mu.Unlock()
		return
	}
	e.phase = RuntimeIdle
	e.mu.Unlock()
	if e.runtime != nil {
		e.runtime.NoteExecution(e.gen, RuntimeIdle, "")
	}
}

func TestUnbindDoesNotClearNewerExecutionGeneration(t *testing.T) {
	_, runtime := reviewRuntime(t)
	first := &testExecution{phase: RuntimeRunning, runtime: runtime}
	second := &testExecution{phase: RuntimeRunning, runtime: runtime}
	first.gen = runtime.BindExecution(first)
	runtime.NoteExecution(first.gen, RuntimeRunning, "old")
	second.gen = runtime.BindExecution(second)
	runtime.NoteExecution(second.gen, RuntimeRunning, "new")
	runtime.UnbindExecution(first.gen)
	if !runtime.Cancel() {
		t.Fatal("new generation lost cancel after old unbind")
	}
	if got := runtime.StateSnapshot().Phase; got != RuntimeCancelling {
		t.Fatalf("phase = %s, want cancelling", got)
	}
	runtime.NoteExecution(first.gen, RuntimeIdle, "")
	if got := runtime.StateSnapshot().Phase; got != RuntimeCancelling {
		t.Fatalf("old generation cleared new phase: %s", got)
	}
	second.Finish()
}

func TestSessionAcceptsCancelHistoryWhileCancelling(t *testing.T) {
	_, runtime := reviewRuntime(t)
	_, exec := bindTestExecution(t, runtime, "turn")
	if !runtime.Cancel() {
		t.Fatal("cancel")
	}
	payload := []byte(`{"messages":[],"reason":"cancel-or-recovery-rewrite"}`)
	if _, err := runtime.Session().Append(t.Context(), Batch{
		OperationID: "history-replace",
		TurnID:      "turn-1",
		Events:      []Event{{Kind: "history/replace", Payload: payload}},
	}); err != nil {
		t.Fatalf("history/replace during cancel: %v", err)
	}
	if _, err := runtime.Session().Append(t.Context(), Batch{
		OperationID: "turn-end",
		TurnID:      "turn-1",
		Events:      []Event{{Kind: "turn/end", Payload: []byte(`{"status":"interrupted"}`)}},
	}); err != nil {
		t.Fatalf("turn/end during cancel: %v", err)
	}
	exec.Finish()
	if got := runtime.StateSnapshot().Phase; got != RuntimeIdle {
		t.Fatalf("phase after finish = %s", got)
	}
}

func TestOldControllerCannotIdleNewGeneration(t *testing.T) {
	_, runtime := reviewRuntime(t)
	old := &testExecution{phase: RuntimeRunning, runtime: runtime}
	old.gen = runtime.BindExecution(old)
	runtime.NoteExecution(old.gen, RuntimeRunning, "old")
	next := &testExecution{phase: RuntimeIdle, runtime: runtime}
	next.gen = runtime.BindExecution(next)
	runtime.NoteExecution(next.gen, RuntimeRunning, "new")
	old.Finish()
	if got := runtime.StateSnapshot().Phase; got != RuntimeRunning {
		t.Fatalf("old finish cleared new turn: %s", got)
	}
	next.Finish()
}
