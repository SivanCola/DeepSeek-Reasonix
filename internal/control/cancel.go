package control

import (
	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/session"
)

// CancelReceipt acknowledges a session-scoped Stop request. Accepted means the
// cancellation signal was processed; it does not claim that every owned
// operation has already exited.
type CancelReceipt struct {
	SessionRef       string `json:"sessionRef"`
	HeadID           string `json:"headId"`
	RuntimeEpoch     string `json:"runtimeEpoch"`
	Accepted         bool   `json:"accepted"`
	AlreadyIdle      bool   `json:"alreadyIdle"`
	RecoveryRequired bool   `json:"recoveryRequired"`
}

// CancelSession stops the activity owned by this captured controller. Callers
// do not need a turn id, and an idle cancellation is idempotently successful.
func (c *Controller) CancelSession() CancelReceipt {
	if c == nil {
		return CancelReceipt{Accepted: true, AlreadyIdle: true}
	}
	c.mu.Lock()
	alreadyIdle := c.turns.cancel == nil && !c.bodyActiveLocked() && !c.finalizingLocked()
	sessionRef := c.sessionPath
	c.mu.Unlock()
	headID := agent.BranchID(sessionRef)
	c.runtimeState.mu.Lock()
	epoch := c.runtimeState.snapshot.RuntimeEpoch
	recoveryRequired := c.runtimeState.snapshot.Phase == "recovery_required"
	c.runtimeState.mu.Unlock()
	service, runtime, exclusive := c.v3Binding()
	if exclusive && runtime != nil {
		sessionRef = runtime.Ref().SessionID
		headID = ""
	}
	cancelled := c.signalTurnCancel()
	if exclusive && runtime != nil && service != nil {
		if v3Receipt, err := service.CancelSession(runtime.Ref()); err == nil {
			epoch = v3Receipt.RuntimeEpoch
			alreadyIdle = v3Receipt.Phase == session.RuntimeIdle
			recoveryRequired = v3Receipt.Phase == session.RuntimeRecoveryRequired
		}
	}
	if cancelled {
		alreadyIdle = false
	}
	go c.finishCancellation(cancelled)
	receipt := CancelReceipt{
		SessionRef: sessionRef, HeadID: headID, RuntimeEpoch: epoch,
		Accepted: true, AlreadyIdle: alreadyIdle, RecoveryRequired: recoveryRequired,
	}
	return receipt
}

// Cancel aborts the in-flight turn. A goroutine blocked awaiting approval
// unblocks via the cancelled context.
func (c *Controller) Cancel() {
	turnID, cancelled := c.cancelTurnLocked()
	c.finishCancel(turnID, cancelled)
}

// cancelLocked is retained for call sites already inside a typed prompt
// transition. Cancellation itself is independent of answer serialization.
func (c *Controller) cancelLocked() {
	turnID, cancelled := c.cancelTurnLocked()
	c.finishCancel(turnID, cancelled)
}

// cancelTurnLocked signals the turn before any observable work: the status
// emit that follows is a synchronous event barrier, and a stalled event lane
// must never keep the provider stream or a tool process alive after Stop.
func (c *Controller) cancelTurnLocked() (string, bool) {
	cancelled := c.signalTurnCancel()
	if !cancelled {
		return "", false
	}
	turnID := ""
	if ledger := c.turnEventLedger(); ledger != nil {
		turnID = ledger.ActiveTurnID()
	}
	c.promptOwner.CancelAll()
	c.approval.clearAll()
	return turnID, true
}

func (c *Controller) finishCancellation(cancelled bool) {
	turnID := ""
	if ledger := c.turnEventLedger(); ledger != nil {
		turnID = ledger.ActiveTurnID()
	}
	c.promptOwner.CancelAll()
	c.approval.clearAll()
	c.finishCancel(turnID, cancelled)
}

func (c *Controller) finishCancel(turnID string, cancelled bool) {
	defer c.refreshRuntimeState(event.Event{})
	if cancelled {
		c.emitTurnStatus(event.TurnCancelling, turnID)
	}
	if c.goals.active() {
		c.stopGoal(GoalStatusStopped)
	}
	if c.sessionEngineEnabled() {
		c.disarmGoalLifecycle("cancelled")
	}
}
