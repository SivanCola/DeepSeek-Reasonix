package control

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/session"
	"reasonix/internal/tool"
)

func exclusiveTestController(t *testing.T, sink event.Sink) (*Controller, *session.Service, *session.Runtime) {
	t.Helper()
	if sink == nil {
		sink = event.Discard
	}
	service, err := session.NewService("desktop", session.NewFilesystemPersistence(filepath.Join(t.TempDir(), "sessions-v4")))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: "loop"})
	if err != nil {
		t.Fatal(err)
	}
	exec := agent.New(testutil.NewMock("test"), tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, sink)
	c := newOwnedTestController(t, Options{
		Runner: exec, Executor: exec, Sink: sink,
		SessionService: service, SessionRuntime: runtime, ExclusiveSession: true,
	})
	return c, service, runtime
}

func TestIdleCancelThenSendProducesNewTurnID(t *testing.T) {
	done := make(chan event.Event, 4)
	c, _, runtime := exclusiveTestController(t, event.FuncSink(func(e event.Event) {
		if e.Kind == event.TurnDone {
			done <- e
		}
	}))
	receipt := c.CancelSession()
	if !receipt.Accepted || !receipt.AlreadyIdle {
		t.Fatalf("idle cancel = %+v", receipt)
	}
	started := make(chan struct{})
	if got := c.runGuarded(func(context.Context) error {
		close(started)
		return nil
	}); got != turnStarted {
		t.Fatalf("send after idle cancel = %v", got)
	}
	<-started
	terminal := waitTurnDoneEvent(t, done)
	if terminal.TurnID == "" {
		t.Fatal("first turn after idle cancel has no durable turn id")
	}
	waitIdleAdmission(t, c)
	if runtime.StateSnapshot().Phase != session.RuntimeIdle {
		t.Fatalf("runtime phase = %s", runtime.StateSnapshot().Phase)
	}
}

func TestCancelDuringStreamingAndToolAndApproval(t *testing.T) {
	for _, name := range []string{"stream", "tool", "approval"} {
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{})
			c := newOwnedTestController(t, Options{Sink: event.Discard})
			t.Cleanup(c.Close)
			c.runGuarded(func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			})
			<-started
			c.CancelSession()
			waitIdleAdmission(t, c)
		})
	}
}

func TestInputAfterCancelWakesOnceFIFO(t *testing.T) {
	var ran atomic.Int32
	order := make(chan int, 2)
	started := make(chan struct{})
	release := make(chan struct{})
	c := newOwnedTestController(t, Options{Sink: event.Discard})
	t.Cleanup(c.Close)
	c.runGuarded(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		<-release
		return ctx.Err()
	})
	<-started
	c.CancelSession()
	if got := c.runGuarded(func(context.Context) error {
		ran.Add(1)
		order <- 1
		return nil
	}); got != turnParked {
		t.Fatalf("first queued = %v, want parked", got)
	}
	if got := c.runGuarded(func(context.Context) error {
		ran.Add(1)
		order <- 2
		return nil
	}); got != turnParked {
		t.Fatalf("second queued = %v, want parked", got)
	}
	close(release)
	waitIdleAdmission(t, c)
	if ran.Load() != 2 {
		t.Fatalf("queued bodies ran %d times, want 2", ran.Load())
	}
	if first, second := <-order, <-order; first != 1 || second != 2 {
		t.Fatalf("fifo order = %d,%d", first, second)
	}
}

func TestSlowExitDoesNotStartNextTurn(t *testing.T) {
	started := make(chan struct{})
	exit := make(chan struct{})
	nextStarted := make(chan struct{})
	c := newOwnedTestController(t, Options{Sink: event.Discard})
	t.Cleanup(c.Close)
	c.runGuarded(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		<-exit
		return ctx.Err()
	})
	<-started
	c.CancelSession()
	if got := c.runGuarded(func(context.Context) error {
		close(nextStarted)
		return nil
	}); got != turnParked {
		t.Fatalf("next admission = %v, want parked until slow exit", got)
	}
	select {
	case <-nextStarted:
		t.Fatal("next turn started before the cancelled body exited")
	case <-time.After(50 * time.Millisecond):
	}
	close(exit)
	select {
	case <-nextStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("queued turn did not start after slow exit")
	}
	waitIdleAdmission(t, c)
}

func TestDuplicateStopAndConcurrentFinish(t *testing.T) {
	started := make(chan struct{})
	c := newOwnedTestController(t, Options{Sink: event.Discard})
	t.Cleanup(c.Close)
	c.runGuarded(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	<-started
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.CancelSession()
			c.Cancel()
		}()
	}
	wg.Wait()
	waitIdleAdmission(t, c)
}

func TestEachStartedTurnHasOneTerminalEvent(t *testing.T) {
	var terminals atomic.Int32
	c := newOwnedTestController(t, Options{Sink: event.FuncSink(func(e event.Event) {
		if e.Kind == event.TurnDone {
			terminals.Add(1)
		}
	})})
	t.Cleanup(c.Close)
	for range 3 {
		c.runGuarded(func(context.Context) error { return nil })
		waitIdleAdmission(t, c)
	}
	if got := terminals.Load(); got != 3 {
		t.Fatalf("terminal events = %d, want 3", got)
	}
}

func TestStopHistoryReplaceThenSendGetsNewTurnID(t *testing.T) {
	done := make(chan event.Event, 8)
	c, _, runtime := exclusiveTestController(t, event.FuncSink(func(e event.Event) {
		if e.Kind == event.TurnDone {
			done <- e
		}
	}))
	started := make(chan struct{})
	c.runGuarded(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		c.replaceSessionAfterCancel(c.executor.Session().Snapshot())
		return ctx.Err()
	})
	<-started
	c.CancelSession()
	first := waitTurnDoneEvent(t, done)
	if first.TurnID == "" {
		t.Fatal("cancelled turn has no durable id")
	}
	waitIdleAdmission(t, c)
	if err := c.turnEventLedgerError(); err != nil {
		t.Fatalf("cancel poisoned admission: %v", err)
	}
	secondStarted := make(chan struct{})
	if got := c.runGuarded(func(context.Context) error {
		close(secondStarted)
		return nil
	}); got != turnStarted {
		t.Fatalf("resend after cancel = %v", got)
	}
	<-secondStarted
	second := waitTurnDoneEvent(t, done)
	if second.TurnID == "" || second.TurnID == first.TurnID {
		t.Fatalf("second turn id = %q, first = %q", second.TurnID, first.TurnID)
	}
	if runtime.StateSnapshot().Phase != session.RuntimeIdle {
		t.Fatalf("runtime phase = %s", runtime.StateSnapshot().Phase)
	}
}

func TestPlanStateTailWriteDuringStopDoesNotPoison(t *testing.T) {
	c, _, runtime := exclusiveTestController(t, event.Discard)
	started := make(chan struct{})
	c.runGuarded(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		if err := c.appendDomainState("plan/state", []byte(`{"enabled":true}`), "stop-race"); err != nil {
			t.Errorf("plan/state during stop: %v", err)
		}
		return ctx.Err()
	})
	<-started
	c.CancelSession()
	waitIdleAdmission(t, c)
	if err := c.turnEventLedgerError(); err != nil {
		t.Fatalf("plan/state vs stop poisoned ledger: %v", err)
	}
	if got := c.runGuarded(func(context.Context) error { return nil }); got != turnStarted {
		t.Fatalf("admission after plan/state race = %v", got)
	}
	waitIdleAdmission(t, c)
	if runtime.StateSnapshot().Phase != session.RuntimeIdle {
		t.Fatalf("runtime phase = %s", runtime.StateSnapshot().Phase)
	}
}

func TestTimeoutRecoveryDropsLateEventsFromNewTurn(t *testing.T) {
	c := newOwnedTestController(t, Options{Sink: event.Discard, SessionDir: t.TempDir(), SessionPath: filepath.Join(t.TempDir(), "session.jsonl")})
	t.Cleanup(c.Close)
	c.testCancelGrace = 10 * time.Millisecond
	started := make(chan struct{})
	hold := make(chan struct{})
	c.runGuarded(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		<-hold
		return ctx.Err()
	})
	<-started
	c.CancelSession()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		phase := c.turns.phase
		c.mu.Unlock()
		if phase == session.RuntimeRecoveryRequired {
			break
		}
		time.Sleep(time.Millisecond)
	}
	late := event.Event{Kind: event.ToolResult, TurnID: "not-the-next-turn", Tool: event.Tool{ID: "late", Name: "bash"}}
	if !c.discardLateTurnEvent(late) {
		t.Fatal("late tool result must not apply to a later turn")
	}
	if got := c.runGuarded(func(context.Context) error { return nil }); got != turnDroppedWriteAuthority {
		t.Fatalf("admission during recovery = %v, want blocked", got)
	}
	close(hold)
}

func TestOldControllerUnbindDoesNotClearNewGeneration(t *testing.T) {
	service, err := session.NewService("desktop", session.NewFilesystemPersistence(filepath.Join(t.TempDir(), "sessions-v4")))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: "handoff"})
	if err != nil {
		t.Fatal(err)
	}
	exec := agent.New(testutil.NewMock("test"), tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
	first := newOwnedTestController(t, Options{Executor: exec, Sink: event.Discard, SessionService: service, SessionRuntime: runtime, ExclusiveSession: true})
	first.mu.Lock()
	oldGen := first.turns.generation
	first.mu.Unlock()
	second := newOwnedTestController(t, Options{Executor: exec, Sink: event.Discard, SessionService: service, SessionRuntime: runtime, ExclusiveSession: true})
	t.Cleanup(func() { first.Close(); second.Close() })
	runtime.UnbindExecution(oldGen)
	started := make(chan struct{})
	second.runGuarded(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	<-started
	if !runtime.Cancel() {
		t.Fatal("new generation lost Stop after old unbind")
	}
	waitIdleAdmission(t, second)
}

func TestCloseDropsLatchedWake(t *testing.T) {
	done := make(chan event.Event, 2)
	started := make(chan struct{})
	exit := make(chan struct{})
	nextStarted := make(chan struct{})
	c := newOwnedTestController(t, Options{Sink: event.FuncSink(func(e event.Event) {
		if e.Kind == event.TurnDone {
			done <- e
		}
	})})
	c.runGuarded(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		<-exit
		return ctx.Err()
	})
	<-started
	c.CancelSession()
	if got := c.runGuarded(func(context.Context) error {
		close(nextStarted)
		return nil
	}); got != turnParked {
		t.Fatalf("wake during abort = %v, want parked", got)
	}
	c.Close()
	close(exit)
	waitTurnDoneEvent(t, done)
	select {
	case <-nextStarted:
		t.Fatal("close started a latched wake")
	default:
	}
}

func TestSessionOpenFailureStillFailsClosed(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan event.Event, 1)
	c := newOwnedTestController(t, Options{
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.TurnDone {
				done <- e
			}
		}),
		SessionDir: t.TempDir(), SessionPath: filepath.Join(blocked, "session.jsonl"),
	})
	t.Cleanup(c.Close)
	c.Submit("must fail closed")
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("open failure did not complete the admission attempt")
	}
	if err := c.turnEventLedgerError(); err == nil {
		t.Fatal("open failure did not fail closed")
	}
}
