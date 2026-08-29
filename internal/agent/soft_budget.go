package agent

import (
	"fmt"

	"reasonix/internal/event"
)

const (
	readonlySoftBudgetRounds = 10
	softBudgetHardFollowup   = 2
)

func (a *Agent) applySoftBudget(outcomes []toolOutcome) intervention {
	if a == nil {
		return intervention{}
	}
	limit := a.task.budget.limit
	if limit.Tokens > 0 || limit.Wall > 0 || limit.Cost > 0 {
		return intervention{}
	}
	if !a.readonlySoftBudgetApplies(outcomes) {
		return intervention{}
	}
	rounds := a.turn.budget.rounds
	if rounds < readonlySoftBudgetRounds {
		return intervention{}
	}
	if a.turn.loop.markSoftBudgetNudged() {
		if a.capabilityAudit != nil {
			a.capabilityAudit.RecordLoopGuard("soft_budget")
		}
		return intervention{
			verdict:  verdictRedirect,
			guidance: "Host budget check: this read-only planning/analysis task has used 10 main-model rounds. Stop expanding scope. Summarize the evidence you already have, state the remaining real blocker if any, and do not open new exploration paths unless the user asks.",
			notice:   noticeFor(event.NoticeCodeLoopGuard, event.LevelInfo, "Converging a long read-only investigation.", fmt.Sprintf("soft budget after %d rounds", rounds)),
		}
	}
	if rounds >= readonlySoftBudgetRounds+softBudgetHardFollowup {
		return intervention{
			verdict:  verdictRedirect,
			guidance: "Host budget check: two further rounds passed after the convergence nudge. Output the current result or name exactly one real blocker. Do not continue exploring.",
		}
	}
	return intervention{}
}

func (a *Agent) readonlySoftBudgetApplies(outcomes []toolOutcome) bool {
	if a.planMode.Load() {
		return true
	}
	for _, outcome := range outcomes {
		if outcome.workspaceMutation != nil || (outcome.resolved && !outcome.resolvedReadOnly) {
			return false
		}
	}
	if a.task.ledger == nil {
		return true
	}
	for _, rec := range a.task.ledger.Receipts() {
		if rec.Write {
			return false
		}
	}
	return true
}
