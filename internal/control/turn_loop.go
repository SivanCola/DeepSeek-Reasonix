package control

import (
	"context"
	"errors"
	"fmt"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/extension"
	"reasonix/internal/provider"
	"reasonix/internal/session"
)

type queuedTurnKind int

const (
	queuedUser queuedTurnKind = iota
	queuedGoal
)

type queuedTurn struct {
	kind      queuedTurnKind
	body      func(ctx context.Context) error
	onStart   func()
	goalRound *goalRoundReservation
}

// turnLoop is the session-scoped execution authority. Controller.mu guards it.
type turnLoop struct {
	phase           session.RuntimePhase
	cancel          context.CancelFunc
	done            chan struct{}
	turnID          string
	token           uint64
	lastToken       uint64
	pending         []queuedTurn
	wake            bool
	generation      uint64
	runtime         *session.Runtime
	cancelRequested bool
	finishingBound  turnFinishingBoundary
}

type controllerExecution struct {
	c *Controller
}

func (e controllerExecution) Snapshot() session.RuntimeSnapshot {
	if e.c == nil {
		return session.RuntimeSnapshot{Phase: session.RuntimeIdle}
	}
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	return session.RuntimeSnapshot{Phase: e.c.turns.phase, Activity: e.c.turns.activityNameLocked()}
}

func (e controllerExecution) Cancel() bool {
	if e.c == nil {
		return false
	}
	return e.c.signalTurnCancel()
}

func (t *turnLoop) activityNameLocked() string {
	switch t.phase {
	case session.RuntimeCancelling:
		return "cancelling"
	case session.RuntimeRecoveryRequired:
		return "recovery_required"
	case session.RuntimeRunning, session.RuntimeFinalizing:
		if t.cancelRequested {
			return "cancelling"
		}
		return "turn"
	default:
		return ""
	}
}

func (c *Controller) bodyActiveLocked() bool {
	switch c.turns.phase {
	case session.RuntimeRunning, session.RuntimeCancelling:
		return true
	default:
		return false
	}
}

func (c *Controller) finalizingLocked() bool {
	return c.turns.phase == session.RuntimeFinalizing
}

func (c *Controller) cancelRequestedLocked() bool {
	if c.closed {
		return false
	}
	return c.turns.cancelRequested || c.turns.phase == session.RuntimeCancelling
}

func (c *Controller) recoveryRequiredLocked() bool {
	return c.turns.phase == session.RuntimeRecoveryRequired
}

func (c *Controller) bindExecutionControl() {
	_, runtime, exclusive := c.v3Binding()
	if !exclusive || runtime == nil {
		return
	}
	snap := runtime.StateSnapshot()
	gen := runtime.BindExecution(controllerExecution{c: c})
	c.executionGeneration.Store(gen)
	c.mu.Lock()
	c.turns.generation = gen
	c.turns.runtime = runtime
	if snap.Phase == session.RuntimeRecoveryRequired {
		c.turns.phase = session.RuntimeRecoveryRequired
	}
	c.mu.Unlock()
}

func (c *Controller) unbindExecutionControl(runtime *session.Runtime) {
	if runtime == nil {
		return
	}
	c.mu.Lock()
	gen := c.turns.generation
	c.mu.Unlock()
	runtime.UnbindExecution(gen)
}

func (c *Controller) noteExecutionLocked(phase session.RuntimePhase, activity string) {
	if c.turns.runtime == nil || c.turns.generation == 0 {
		return
	}
	c.turns.runtime.NoteExecution(c.turns.generation, phase, activity)
}

func (c *Controller) currentTurnToken() (token uint64, turnID string, active bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.turns.phase {
	case session.RuntimeIdle, session.RuntimeClosed:
		return c.turns.lastToken, "", false
	default:
		return c.turns.token, c.turns.turnID, true
	}
}

func (c *Controller) discardLateTurnEvent(e event.Event) bool {
	if e.TurnID == "" || !lateBusinessEvent(e.Kind) {
		return false
	}
	_, turnID, active := c.currentTurnToken()
	if active {
		return e.TurnID != turnID
	}
	return true
}

func (c *Controller) startTurnLocked(next queuedTurn) (ctx context.Context, cancel context.CancelFunc) {
	ctx, cancel = context.WithCancel(extension.ContextWithRuntimeOwner(context.Background(), c.runtimeOwner))
	c.turns.cancel = cancel
	c.turns.done = make(chan struct{})
	c.turns.phase = session.RuntimeRunning
	c.turns.cancelRequested = false
	c.turns.token++
	c.turns.turnID = ""
	c.noteExecutionLocked(session.RuntimeRunning, "turn")
	return ctx, cancel
}

func (c *Controller) popNextPendingLocked() (queuedTurn, bool) {
	if len(c.turns.pending) == 0 {
		c.turns.wake = false
		return queuedTurn{}, false
	}
	next := c.turns.pending[0]
	c.turns.pending = c.turns.pending[1:]
	c.turns.wake = len(c.turns.pending) > 0
	return next, true
}

func (c *Controller) queueTurnLocked(item queuedTurn) {
	// Harness wakeRequested: input that cannot join the current activity is
	// claimed once when the body converges. Close clears this queue so a
	// disposed session never starts a latched turn.
	c.turns.pending = append(c.turns.pending, item)
	c.turns.wake = true
}

func (c *Controller) signalTurnCancel() bool {
	c.mu.Lock()
	cancel := c.turns.cancel
	first := cancel != nil && c.turns.phase == session.RuntimeRunning
	if cancel != nil && (c.turns.phase == session.RuntimeRunning || c.turns.phase == session.RuntimeCancelling) {
		c.turns.phase = session.RuntimeCancelling
		c.turns.cancelRequested = true
	}
	done := c.turns.done
	c.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	if first {
		c.startCancellationWatchdog(done)
	}
	return true
}

func (c *Controller) enterRecoveryLocked(reason string) {
	c.turns.phase = session.RuntimeRecoveryRequired
	c.noteExecutionLocked(session.RuntimeRecoveryRequired, reason)
	if c.turns.runtime != nil {
		c.turns.runtime.RequireRecovery(reason)
	}
}

func (c *Controller) spawnGuardedTurn(ctx context.Context, cancel context.CancelFunc, body func(ctx context.Context) error, goalRound *goalRoundReservation) {
	ctx, completion := withGuardedTurnCompletion(ctx)
	body = c.prepareTurnAdmissionWithGoalRound(body, goalRound)
	if ledger := c.turnEventLedger(); ledger != nil {
		c.mu.Lock()
		c.turns.turnID = ledger.ActiveTurnID()
		c.mu.Unlock()
	}
	c.liveness.reset(time.Now())
	c.autosaveWG.Go(func() {
		c.autosaveWhileRunning(ctx)
	})
	go func() {
		defer cancel()
		defer func() {
			c.finishGoalRoundActivity(goalRound)
			c.kickGoalDriver()
		}()
		defer func() {
			if r := recover(); r != nil {
				err := fmt.Errorf("internal error: %v", r)
				goalRound.setResult(err, false)
				c.finishGuardedTurn(err, completion)
			}
		}()
		err := body(ctx)
		if goalRound != nil {
			goalRound.setResult(err, errors.Is(ctx.Err(), context.Canceled) && c.CancelRequested())
		}
		c.finishGuardedTurn(explainError(err), completion)
	}()
}

func (c *Controller) cancellationGrace() time.Duration {
	if c != nil && c.testCancelGrace > 0 {
		return c.testCancelGrace
	}
	return 15 * time.Second
}

func (c *Controller) finishGuardedTurn(err error, completion *guardedTurnCompletion) {
	c.memory.clearAutoRemember()
	c.mu.Lock()
	cancelRequested := c.turns.cancelRequested
	if c.turns.done != nil {
		close(c.turns.done)
		c.turns.done = nil
	}
	if c.closed {
		c.turns.phase = session.RuntimeClosed
		c.turns.cancel = nil
		c.turns.cancelRequested = false
		c.turns.finishingBound.end()
		c.noteExecutionLocked(session.RuntimeIdle, "")
		c.mu.Unlock()
		c.emitTurnDoneEvent(err, true, completion)
		c.refreshRuntimeState(event.Event{})
		return
	}
	if c.turns.phase == session.RuntimeRecoveryRequired {
		c.turns.cancel = nil
		c.mu.Unlock()
		c.emitTurnDoneEvent(err, cancelRequested, completion)
		c.refreshRuntimeState(event.Event{})
		return
	}
	c.turns.phase = session.RuntimeFinalizing
	c.turns.finishingBound.begin(true)
	c.turns.cancel = nil
	c.noteExecutionLocked(session.RuntimeFinalizing, "turn")
	c.mu.Unlock()

	c.refreshRuntimeState(event.Event{})
	defer func() {
		c.mu.Lock()
		c.turns.finishingBound.end()
		c.turns.cancelRequested = false
		if c.closed {
			c.turns.phase = session.RuntimeClosed
			c.mu.Unlock()
			c.refreshRuntimeState(event.Event{})
			return
		}
		if c.turns.phase == session.RuntimeRecoveryRequired {
			c.mu.Unlock()
			c.refreshRuntimeState(event.Event{})
			return
		}
		if ledger := c.turnEventLedger(); ledger != nil && ledger.CurrentStatus() == event.TurnRecoveryRequired {
			c.enterRecoveryLocked("terminal")
			c.mu.Unlock()
			c.refreshRuntimeState(event.Event{})
			return
		}
		next, ok := c.popNextPendingLocked()
		if !ok {
			c.turns.lastToken = c.turns.token
			c.turns.phase = session.RuntimeIdle
			c.turns.turnID = ""
			c.noteExecutionLocked(session.RuntimeIdle, "")
			c.mu.Unlock()
			c.maybeDispatchInbox()
			c.refreshRuntimeState(event.Event{})
			return
		}
		ctx, cancel := c.startTurnLocked(next)
		c.mu.Unlock()
		if next.onStart != nil {
			next.onStart()
		}
		c.spawnGuardedTurn(ctx, cancel, next.body, next.goalRound)
		c.refreshRuntimeState(event.Event{})
	}()
	c.emitTurnDoneEvent(err, cancelRequested, completion)
}

func (c *Controller) emitTurnDoneEvent(err error, cancelRequested bool, completion *guardedTurnCompletion) {
	c.inbox.mu.Lock()
	activeInboxID := ""
	for id := range c.inbox.activeItemIDs {
		activeInboxID = id
		break
	}
	c.inbox.mu.Unlock()
	done := event.Event{
		Kind:           event.TurnDone,
		Err:            err,
		Cancelled:      cancelRequested,
		Outcome:        turnOutcome(err),
		CheckpointTurn: c.validatedCheckpointTurn(completion),
		Receipt:        c.executor.CompletionReceipt(),
		ItemID:         activeInboxID,
	}
	if done.CheckpointTurn != nil {
		changes := completion.checkpoint.store.FreezeTurnChanges(*done.CheckpointTurn)
		if done.Receipt == nil && (len(changes.Files) > 0 || len(changes.Reasons) > 0) {
			done.Receipt = &event.CompletionReceipt{AssessmentKind: "facts", Verdict: "unknown"}
		}
		if done.Receipt != nil {
			receipt := *done.Receipt
			receipt.Diff = changes.Summary()
			receipt.Interrupted = cancelRequested
			done.Receipt = &receipt
		}
	}
	done.Receipt = bindCompletionLogSources(done.Receipt, c.History())
	done = c.applyTurnDoneProtocol(done, cancelRequested)
	c.applyToolRecoveryTurnStatus(&done, completion)
	var readErr *agent.IncompleteReadError
	if errors.As(err, &readErr) {
		done.ReadPause = readErr.Pause
	}
	done.Diagnostic = provider.DiagnoseFailure(err)
	done.Detail = provider.FailureDiagnosticDetail(done.Diagnostic)
	if !cancelRequested {
		done.ProtocolRecovery = c.executor.PendingProtocolRecovery()
	}
	var readinessErr *agent.FinalReadinessError
	if errors.As(err, &readinessErr) {
		done.Readiness = &event.FinalReadiness{Attempts: readinessErr.Attempts, Missing: append([]string(nil), readinessErr.Missing...)}
	}
	c.onInboxTurnDone()
	c.sink.Emit(done)
}

func (c *Controller) startCancellationWatchdog(done chan struct{}) {
	if c == nil || done == nil {
		return
	}
	go func() {
		timer := time.NewTimer(c.cancellationGrace())
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-done:
			return
		}

		c.mu.Lock()
		stillRunning := !c.closed && (c.turns.phase == session.RuntimeRunning || c.turns.phase == session.RuntimeCancelling)
		turnID := c.turns.turnID
		if stillRunning {
			c.enterRecoveryLocked("cancellation_grace_expired")
		}
		c.mu.Unlock()
		if !stillRunning {
			return
		}
		if turnID == "" {
			if ledger := c.turnEventLedger(); ledger != nil {
				turnID = ledger.ActiveTurnID()
			}
		}
		recovery := &event.RecoveryStatus{
			State:                "recovery_required",
			Phase:                "cancellation_grace_expired",
			Reason:               "cancellation_grace_expired",
			RequiresUserDecision: true,
		}
		_ = c.emitTurnEventChecked(event.Event{
			Kind:      event.TurnDone,
			TurnID:    turnID,
			Status:    event.TurnRecoveryRequired,
			Cancelled: true,
			Outcome:   "unknown",
			Recovery:  recovery,
		})
		c.refreshRuntimeState(event.Event{})
	}()
}
