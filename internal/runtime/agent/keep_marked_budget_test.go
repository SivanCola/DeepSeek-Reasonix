package agent

import (
	"fmt"
	"strings"
	"testing"

	"reasonix/internal/contract/provider"
)

func markedTurn(n, chars int) provider.Message {
	return provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("[[keep]] turn %d ", n) + strings.Repeat("c", chars)}
}

func keptMarkedTokens(kept []provider.Message) (tokens int, turns map[string]bool) {
	turns = map[string]bool{}
	for _, m := range kept {
		if m.Role == provider.RoleUser && isUserMarked(m) {
			tokens += fixedTokenEstimate(m)
			turns[strings.Fields(m.Content)[2]] = true
		}
	}
	return tokens, turns
}

// [[keep]] holds a turn verbatim inside a budget of its own. With no bound a
// session that marks turn after turn grows what every fold must carry until the
// candidate cannot land under the trigger, and a fold that cannot land is
// refused again every round.
func TestMarkedTurnsStayWithinTheirBudgetAndTheNewestSurvive(t *testing.T) {
	a := &Agent{agentConfig: agentConfig{contextWindow: 200_000}, keepPolicy: KeepUserMarked}
	budget := a.window().keptMarkedBudget()
	if budget <= 0 {
		t.Fatalf("budget = %d, want a positive bound", budget)
	}
	var region []provider.Message
	for i := range 20 {
		region = append(region, markedTurn(i, 40_000))
	}
	kept, fold, retention, _ := a.window().partitionFoldWith(region, nil, a.keepPolicy)
	tokens, turns := keptMarkedTokens(kept)
	if tokens > budget {
		t.Fatalf("%d tokens of [[keep]] turns kept, budget %d", tokens, budget)
	}
	if !turns["19"] || turns["0"] {
		t.Fatalf("kept %v, want the newest turn kept and the oldest folded", turns)
	}
	if retention.MarkedDropped == 0 || retention.MarkedDropped != 20-len(turns) || len(fold) != retention.MarkedDropped {
		t.Fatalf("retention reports %d dropped marked turns, fold has %d messages, %d kept", retention.MarkedDropped, len(fold), len(turns))
	}
}

func TestMarkedTurnsUnderTheirBudgetAllStay(t *testing.T) {
	a := &Agent{agentConfig: agentConfig{contextWindow: 200_000}, keepPolicy: KeepUserMarked}
	var region []provider.Message
	for i := range 4 {
		region = append(region, markedTurn(i, 2_000))
	}
	kept, fold, retention, _ := a.window().partitionFoldWith(region, nil, a.keepPolicy)
	if _, turns := keptMarkedTokens(kept); len(turns) != 4 || len(fold) != 0 || retention.MarkedDropped != 0 {
		t.Fatalf("kept %v, fold %d, dropped %d; want all four kept", turns, len(fold), retention.MarkedDropped)
	}
}

// Marked turns carried through folds must not accumulate: a turn kept at one
// fold rides into the next fold's region.
func TestMarkedTurnsCarriedThroughFoldsDoNotAccumulate(t *testing.T) {
	a := &Agent{agentConfig: agentConfig{contextWindow: 200_000}, keepPolicy: KeepUserMarked}
	budget := a.window().keptMarkedBudget()
	var carried []provider.Message
	next := 0
	for round := 1; round <= 8; round++ {
		region := []provider.Message{formatSummaryMessage("earlier work")}
		region = append(region, carried...)
		for range 5 {
			region = append(region, markedTurn(next, 40_000))
			next++
		}
		kept, _, _, _ := a.window().partitionFoldWith(region, nil, a.keepPolicy)
		if tokens, _ := keptMarkedTokens(kept); tokens > budget {
			t.Fatalf("round %d: %d tokens kept, budget %d", round, tokens, budget)
		}
		carried = kept
	}
}
