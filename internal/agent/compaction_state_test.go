package agent

import (
	"strings"
	"testing"
)

func TestRenderCompactionStateDeterministic(t *testing.T) {
	st := CompactionState{
		ActiveGoal: "ship feature",
		GoalStatus: "running",
		Todos: []CompactionTodo{
			{ID: "2", Status: "pending", Text: "b"},
			{ID: "1", Status: "in_progress", Text: "a"},
		},
		CompletedTodos: 3,
		EditedPaths:    []string{"z.go", "a.go"},
	}
	a := RenderCompactionState(st)
	b := RenderCompactionState(st)
	if a != b {
		t.Fatal("not deterministic")
	}
	if !strings.Contains(a, "- a.go") || !strings.Contains(a, "- z.go") {
		t.Fatalf("paths not sorted:\n%s", a)
	}
	// a.go should appear before z.go
	if strings.Index(a, "- a.go") > strings.Index(a, "- z.go") {
		t.Fatal("edited paths must be sorted")
	}
	if !strings.HasPrefix(strings.TrimSpace(a), "<compaction-state") {
		t.Fatal("missing open tag")
	}
}

func TestRenderCompactionStateTruncates(t *testing.T) {
	st := CompactionState{ActiveGoal: strings.Repeat("x", maxCompactionStateBytes)}
	out := RenderCompactionState(st)
	if len(out) > maxCompactionStateBytes+128 {
		t.Fatalf("len=%d still too large", len(out))
	}
	if !strings.Contains(out, "sha256=") {
		t.Fatal("expected digest on truncate")
	}
}

func TestCompactionMessagesSynthetic(t *testing.T) {
	sum := CompactionSummaryMessage("## Goal\nok")
	if sum.SyntheticReason != "compaction_summary" {
		t.Fatalf("reason=%q", sum.SyntheticReason)
	}
	st := CompactionStateMessage(CompactionState{ActiveGoal: "g"})
	if st.SyntheticReason != "compaction_state" {
		t.Fatalf("reason=%q", st.SyntheticReason)
	}
}
