package main

import (
	"fmt"
	"strings"

	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/turnevent"
)

// TurnStartView is the synchronous admission receipt for the new Wails turn
// API. Events remain the streaming authority after admission.
type TurnStartView struct {
	TurnID       string           `json:"turnId"`
	Status       event.TurnStatus `json:"status"`
	RuntimeEpoch string           `json:"runtimeEpoch,omitempty"`
	SubmissionID string           `json:"submissionId,omitempty"`
}

// StartTurnForTab is the turn-id-aware replacement for SubmitToTab. Existing
// Submit entry points remain compatibility wrappers during the protocol cutover.
func (a *App) StartTurnForTab(tabID, input, submissionID string) (TurnStartView, error) {
	if strings.TrimSpace(submissionID) == "" {
		return TurnStartView{}, fmt.Errorf("submissionId is required")
	}
	if err := a.SubmitToTabWithID(tabID, input, submissionID); err != nil {
		return TurnStartView{}, err
	}
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if ctrl == nil {
		return TurnStartView{}, a.workspaceNotReadyErr(tab)
	}
	turnID := ""
	if admitted, ok := ctrl.(interface{ TurnIDForSubmission(string) string }); ok {
		turnID = admitted.TurnIDForSubmission(submissionID)
	}
	if strings.TrimSpace(turnID) == "" {
		return TurnStartView{}, fmt.Errorf("turn admission did not produce a durable turn id")
	}
	epoch := ""
	if tab != nil && tab.sink != nil {
		epoch = tab.sink.runtimeEpochSnapshot()
	}
	// This is an admission receipt, not a potentially raced runtime snapshot.
	// Ordered events carry every later transition, including a provider that
	// completed before the Wails Promise was delivered.
	return TurnStartView{TurnID: turnID, Status: event.TurnQueued, RuntimeEpoch: epoch, SubmissionID: submissionID}, nil
}

// InterruptTurnForTab cancels only the exact active turn. A stale Stop button
// can no longer cancel a replacement turn admitted in the same tab.
func (a *App) InterruptTurnForTab(tabID, turnID string) error {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return fmt.Errorf("turnId is required")
	}
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if ctrl == nil {
		return a.workspaceNotReadyErr(tab)
	}
	status := ctrl.RuntimeStatus()
	if status.TurnID != turnID || !status.Running {
		return fmt.Errorf("turn %q is not the active turn for tab %q", turnID, tabID)
	}
	ctrl.Cancel()
	return nil
}

// InterruptTurnWithInboxItemsForTab is the receipt-capable exact-turn Stop
// used by the Composer when it also discards queued follow-ups.
func (a *App) InterruptTurnWithInboxItemsForTab(tabID, turnID string, itemIDs []string) (InboxCancelResultView, error) {
	view := InboxCancelResultView{DiscardedItemIDs: []string{}}
	turnID = strings.TrimSpace(turnID)
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if ctrl == nil {
		return view, a.workspaceNotReadyErr(tab)
	}
	status := ctrl.RuntimeStatus()
	if turnID == "" || status.TurnID != turnID || !status.Running {
		return view, fmt.Errorf("turn %q is not the active turn for tab %q", turnID, tabID)
	}
	result, err := ctrl.CancelWithInboxItemsResult(itemIDs, "desktop")
	if err != nil {
		return view, inboxWailsError(err)
	}
	view.DiscardedItemIDs = append(view.DiscardedItemIDs, result.DiscardedItemIDs...)
	view.Warning = result.Warning
	a.emitInboxChanged(tabID)
	return view, nil
}

// AnswerPromptForTab resolves an Ask only when it belongs to the exact active
// turn. Controller-side prompt ids remain independently idempotent.
func (a *App) AnswerPromptForTab(tabID, turnID, promptID string, answers []QuestionAnswer) error {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if ctrl == nil {
		return a.workspaceNotReadyErr(tab)
	}
	status := ctrl.RuntimeStatus()
	if strings.TrimSpace(turnID) == "" || status.TurnID != strings.TrimSpace(turnID) {
		return fmt.Errorf("turn %q is not the active turn for tab %q", turnID, tabID)
	}
	// Resolve on the controller instance that passed the turn-id fence. Calling
	// the legacy app wrapper here would re-resolve the tab and could deliver a
	// late answer to a replacement controller after a runtime rebuild.
	out := make([]event.AskAnswer, len(answers))
	for i, answer := range answers {
		out[i] = event.AskAnswer{QuestionID: answer.QuestionID, Selected: answer.Selected}
	}
	ctrl.AnswerQuestion(promptID, out)
	return nil
}

type turnEventReader interface {
	TurnEventsAfter(after uint64) ([]turnevent.Envelope, error)
}

// TurnEventsForTab supplies the durable suffix used to repair sequence gaps or
// rebuild after a runtime epoch change.
func (a *App) TurnEventsForTab(tabID string, afterSeq uint64) ([]turnevent.Envelope, error) {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if ctrl == nil {
		return []turnevent.Envelope{}, a.workspaceNotReadyErr(tab)
	}
	reader, ok := ctrl.(turnEventReader)
	if !ok {
		return []turnevent.Envelope{}, fmt.Errorf("turn event replay is unavailable")
	}
	events, err := reader.TurnEventsAfter(afterSeq)
	if events == nil {
		events = []turnevent.Envelope{}
	}
	return events, err
}

var _ control.SessionAPI = (*control.Controller)(nil)
