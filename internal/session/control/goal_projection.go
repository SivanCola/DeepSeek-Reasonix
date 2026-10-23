// goal_projection.go — what the model has of the goal standing block.
package control

import "strings"

// Why a goal the model was shown stopped. A fixed identity, not prose: each
// reason has exactly one clause, so the same stop renders the same bytes and the
// once-only question can compare them.
const (
	goalStoppedPaused  = "paused"
	goalStoppedCleared = "cleared"
	goalStoppedBudget  = "budget"
	goalStoppedHost    = "host"
)

// goalStoppedClauses is the one clause each reason stands for.
var goalStoppedClauses = map[string]string{
	goalStoppedPaused:  "paused by the user",
	goalStoppedCleared: "cleared by the user",
	goalStoppedBudget:  "its budget is used up",
	goalStoppedHost:    "the host paused it: its completion check could not run",
}

// goalStoppedBlock retires a contract the model was shown. It carries the same
// tag as the contract, so it is the newest copy a fold keeps, and its opening
// carries attributes, so the once-only question compares the whole block.
func goalStoppedBlock(reason string) string {
	clause, ok := goalStoppedClauses[reason]
	if !ok {
		return ""
	}
	return `<active-goal contract="stopped" reason="` + reason + `">` + "\n" +
		"This goal is no longer active (" + clause + "). Anything the goal contract told you earlier no longer applies: do not keep pursuing it or take its next step; wait for the user's instruction.\n" +
		activeGoalClose
}

// goalStopReason names why a goal stopped, empty when the model already knows:
// its own blocked report and a completed goal need no retraction. A pause keeps
// the goal text and records a cause; a clear leaves a stopped goal with none.
func goalStopReason(goal, status, stopCause string) string {
	switch {
	case status == GoalStatusRunning, status == GoalStatusComplete:
		return ""
	case stopCause == stopCauseManual:
		return goalStoppedPaused
	case stopCause == stopCauseBudgetSpend:
		return goalStoppedBudget
	case stopCause == stopCauseEvaluator:
		return goalStoppedHost
	case status == GoalStatusStopped && strings.TrimSpace(goal) == "":
		return goalStoppedCleared
	case status == GoalStatusStopped:
		// A cancelled turn stops the goal without clearing it; resuming it is
		// the user's next decision, so it reads as their pause.
		return goalStoppedPaused
	}
	return ""
}

// goalRetractionBlock is the tombstone a turn owes, empty when the model already
// holds it or never read a goal block. Only a stop the model did not report
// retracts; a rewind or fold that drops the tombstone owes it again.
func (c *Controller) goalRetractionBlock(goal, status, stopCause string) string {
	reason := goalStopReason(goal, status, stopCause)
	if reason == "" || !c.goalBlock.visible(activeGoalTag) {
		return ""
	}
	return c.goalBlock.owedBlock(activeGoalTag, goalStoppedBlock(reason))
}
