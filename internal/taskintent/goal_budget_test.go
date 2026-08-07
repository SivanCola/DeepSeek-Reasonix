package taskintent

import (
	"testing"

	"reasonix/internal/intent/corpus"
)

// TestGoalNeedsWriteBudgetMatrix pins which objectives start a Goal on the
// extended write turn budget. The case table lives in internal/intent/corpus so
// this gate test and the classifier evaluation harness judge one corpus.
func TestGoalNeedsWriteBudgetMatrix(t *testing.T) {
	for _, tt := range corpus.GoalBudget {
		if tt.NeedsWriteBudget == nil {
			continue
		}
		t.Run(tt.Name, func(t *testing.T) {
			if got := GoalNeedsWriteBudget(tt.Text); got != *tt.NeedsWriteBudget {
				t.Errorf("GoalNeedsWriteBudget(%q) = %v, want %v", tt.Text, got, *tt.NeedsWriteBudget)
			}
		})
	}
}

// TestGoalBareFaultDoesNotChangeDeliveryClassification pins ordinary Delivery
// consultation/diagnosis so Goal write inference cannot drift the shared
// delivery gates.
//
// This is the contract that shapes the whole intent design: a bare fault report
// must start a Goal on the write budget while ordinary Delivery still treats it
// as non-mutation, so one sentence has to produce opposite answers for two
// consumers. See internal/intent.TestCrossConsumerDisagreement.
func TestGoalBareFaultDoesNotChangeDeliveryClassification(t *testing.T) {
	saw := 0
	for _, tt := range corpus.GoalBudget {
		if tt.NeedsMutation == nil {
			continue
		}
		saw++
		t.Run(tt.Name, func(t *testing.T) {
			if got := NeedsMutation(tt.Text); got != *tt.NeedsMutation {
				t.Errorf("NeedsMutation(%q) = %v, want %v", tt.Text, got, *tt.NeedsMutation)
			}
		})
	}
	if saw == 0 {
		t.Fatal("no mutation-labeled Goal cases in the corpus; the cross-consumer contract is unguarded")
	}
}

func TestTaskFaultSignalsSharedWithGoalClassification(t *testing.T) {
	// Shared fault list must keep task recognition and Goal classification
	// aligned for bare problem statements.
	for _, phrase := range []string{"bug", "crash", "崩溃", "异常", "报错", "失败"} {
		input := "something " + phrase + " happened"
		if !taskInputHasFaultSignal(input) {
			t.Errorf("taskInputHasFaultSignal missing %q", phrase)
		}
		if !heuristicInputIsTask(input) && !stringsContainsCJK(phrase) {
			// English bare fault remains a task signal.
			if !heuristicInputIsTask("the service has a " + phrase) {
				t.Errorf("heuristic task signal missing for fault %q", phrase)
			}
		}
	}
}

func stringsContainsCJK(s string) bool {
	for _, r := range s {
		if r > 127 {
			return true
		}
	}
	return false
}
