package acp

import "testing"

func TestLoadedGoalDraftModeRequiresMissingGoal(t *testing.T) {
	if loadedGoalDraftMode("finish the restored target") {
		t.Fatal("an existing stopped/disarmed goal was treated as a new-goal draft")
	}
	if !loadedGoalDraftMode("  ") {
		t.Fatal("a session without a goal must keep draft mode for the next prompt")
	}
}
