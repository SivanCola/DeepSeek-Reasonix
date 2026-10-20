package agent

import (
	"fmt"
	"strings"
	"testing"

	"reasonix/internal/contract/provider"
)

func failureGroup(n int, chars int) []provider.Message {
	id := fmt.Sprintf("f%d", n)
	return []provider.Message{
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: id, Name: "bash", Arguments: "{}"}}},
		{Role: provider.RoleTool, ToolCallID: id, Name: "bash", Content: "error: " + fmt.Sprintf("failure %d ", n) + strings.Repeat("x", chars)},
	}
}

func keptFailureTokens(kept []provider.Message) (tokens int, ids map[string]bool) {
	ids = map[string]bool{}
	for _, m := range kept {
		if m.Role == provider.RoleTool && isErrorMessage(m) {
			tokens += fixedTokenEstimate(m)
			ids[m.ToolCallID] = true
		}
	}
	return tokens, ids
}

// Retained failures are carried into the next fold as part of its region, so a
// retention with no budget of its own grows by every round's failures for as
// long as the session lasts.
func TestKeptFailuresStayWithinTheirBudgetAcrossFolds(t *testing.T) {
	a := &Agent{agentConfig: agentConfig{contextWindow: 200_000}, keepPolicy: KeepErrors}
	budget := a.window().keptFailuresBudget()
	if budget <= 0 {
		t.Fatalf("budget = %d, want a positive bound", budget)
	}

	var carried []provider.Message
	next := 0
	for round := 1; round <= 8; round++ {
		region := []provider.Message{formatSummaryMessage("earlier work")}
		region = append(region, carried...)
		for range 6 {
			region = append(region, failureGroup(next, 8000)...)
			next++
		}
		kept, _, _, _ := a.window().partitionFoldWith(region, nil, a.keepPolicy)
		tokens, ids := keptFailureTokens(kept)
		if tokens > budget {
			t.Fatalf("round %d: %d tokens of failures kept, budget %d", round, tokens, budget)
		}
		if newest := fmt.Sprintf("f%d", next-1); !ids[newest] {
			t.Fatalf("round %d: the newest failure %s was folded while older ones stayed: %v", round, newest, ids)
		}
		carried = kept
	}
	if _, ids := keptFailureTokens(carried); ids["f0"] {
		t.Fatalf("the oldest failure is still carried after 8 rounds")
	}
}

func TestKeptFailuresUnderBudgetAllStay(t *testing.T) {
	a := &Agent{agentConfig: agentConfig{contextWindow: 200_000}, keepPolicy: KeepErrors}
	var region []provider.Message
	for i := range 3 {
		region = append(region, failureGroup(i, 400)...)
	}
	kept, fold, _, _ := a.window().partitionFoldWith(region, nil, a.keepPolicy)
	if _, ids := keptFailureTokens(kept); len(ids) != 3 || len(fold) != 0 {
		t.Fatalf("kept failures = %v, fold = %d messages, want all three kept and nothing folded", ids, len(fold))
	}
}

// [[keep]] is the documented way past a retention budget, so a failure budget
// must not reach it.
func TestKeptFailuresBudgetLeavesUserMarkedTurnsAlone(t *testing.T) {
	a := &Agent{agentConfig: agentConfig{contextWindow: 200_000}, keepPolicy: KeepErrors | KeepUserMarked}
	marked := provider.Message{Role: provider.RoleUser, Content: "[[keep]] " + strings.Repeat("constraint ", 5000)}
	region := []provider.Message{marked}
	for i := range 20 {
		region = append(region, failureGroup(i, 8000)...)
	}
	kept, _, _, _ := a.window().partitionFoldWith(region, nil, a.keepPolicy)
	if len(kept) == 0 || kept[0].Content != marked.Content {
		t.Fatalf("the [[keep]] turn did not survive intact among %d kept messages", len(kept))
	}
}
