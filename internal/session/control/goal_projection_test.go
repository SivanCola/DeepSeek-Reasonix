package control

import (
	"strings"
	"testing"
)

// Every reason has one fixed clause, so the same stop always renders the same
// bytes and the once-only question can compare them.
func TestGoalStoppedBlockIsAFixedIdentity(t *testing.T) {
	cases := []struct{ reason, clause string }{
		{goalStoppedPaused, "paused by the user"},
		{goalStoppedCleared, "cleared by the user"},
		{goalStoppedBudget, "its budget is used up"},
		{goalStoppedHost, "the host paused it"},
	}
	for _, tc := range cases {
		block := goalStoppedBlock(tc.reason)
		if !strings.HasPrefix(block, `<active-goal contract="stopped" reason="`+tc.reason+`">`) {
			t.Fatalf("%s: the block does not open with its identity:\n%s", tc.reason, block)
		}
		if !strings.Contains(block, "("+tc.clause) {
			t.Fatalf("%s: the block does not carry its clause %q:\n%s", tc.reason, tc.clause, block)
		}
		if !strings.Contains(block, "Anything the goal contract told you earlier no longer applies") {
			t.Fatalf("%s: the block does not retire the earlier contract:\n%s", tc.reason, block)
		}
		if !strings.HasSuffix(block, activeGoalClose) {
			t.Fatalf("%s: not a closed fragment:\n%s", tc.reason, block)
		}
	}
	if got := goalStoppedBlock("invented"); got != "" {
		t.Fatalf("an unknown reason rendered a block:\n%s", got)
	}
}

// Only a stop the model did not report retracts: its own blocked report and a
// completed goal need no tombstone.
func TestGoalStopReasonRetractsOnlyUnreportedStops(t *testing.T) {
	cases := []struct{ name, goal, status, cause, want string }{
		{"running", "ship it", GoalStatusRunning, "", ""},
		{"model reported blocked", "ship it", GoalStatusBlocked, "", ""},
		{"complete", "", GoalStatusComplete, "", ""},
		{"user paused", "ship it", GoalStatusBlocked, stopCauseManual, goalStoppedPaused},
		{"user cleared", "", GoalStatusStopped, "", goalStoppedCleared},
		{"budget", "ship it", GoalStatusBlocked, stopCauseBudgetSpend, goalStoppedBudget},
		{"completion check unavailable", "ship it", GoalStatusBlocked, stopCauseEvaluator, goalStoppedHost},
		{"cancelled turn", "ship it", GoalStatusStopped, "", goalStoppedPaused},
	}
	for _, tc := range cases {
		if got := goalStopReason(tc.goal, tc.status, tc.cause); got != tc.want {
			t.Errorf("%s: reason = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The tombstone is owed by what the model can see: a contract or reminder in
// view is retired, the tombstone itself is not repeated, and a session that
// never read a goal block is not told one stopped.
func TestGoalRetractionIsOwedByTheVisibleBlock(t *testing.T) {
	full := activeGoalBlock("ship it", true)
	reminder := activeGoalBlock("ship it", false)
	tombstone := goalStoppedBlock(goalStoppedCleared)
	cases := []struct {
		name   string
		newest func(string) (string, bool)
		want   string
	}{
		{"never read a goal block", func(string) (string, bool) { return "", false }, ""},
		{"the contract is in view", func(string) (string, bool) { return full, true }, tombstone},
		{"a reminder is the newest copy", func(string) (string, bool) { return reminder, true }, tombstone},
		{"the tombstone is already newest", func(string) (string, bool) { return tombstone, true }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(Options{})
			defer c.Close()
			c.goalBlock.newest = tc.newest
			if got := c.goalRetractionBlock("", GoalStatusStopped, ""); got != tc.want {
				t.Fatalf("retraction = %q, want %q", got, tc.want)
			}
		})
	}
}
