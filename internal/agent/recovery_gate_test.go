package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/evidence"
)

type recordingRecoveryGate struct {
	observation RecoveryObservation
}

func TestRecoveryPlanTransitionDetectsOnlyStructuralRewriteOfActivePlan(t *testing.T) {
	a := &Agent{}
	initial := json.RawMessage(`{"todos":[{"content":"Implement parser","status":"in_progress"}]}`)
	if changed, _, _ := a.recoveryPlanTransition("todo_write", initial); changed {
		t.Fatal("initial plan must stay on the fast path")
	}

	a.setTodoState([]evidence.TodoItem{
		{Content: "Implement parser", Status: "in_progress"},
		{Content: "Run tests", Status: "pending"},
	})
	progressOnly := json.RawMessage(`{"todos":[{"content":"Implement parser","status":"completed"},{"content":"Run tests","status":"in_progress"}]}`)
	if changed, _, _ := a.recoveryPlanTransition("todo_write", progressOnly); changed {
		t.Fatal("progress-only update must not invoke the plan reviewer")
	}

	replacement := json.RawMessage(`{"todos":[{"content":"Replace parser architecture","status":"in_progress"},{"content":"Run tests","status":"pending"}]}`)
	changed, before, after := a.recoveryPlanTransition("todo_write", replacement)
	if !changed {
		t.Fatal("structural rewrite of active plan was not detected")
	}
	if !strings.Contains(before, "Implement parser") || !strings.Contains(after, "Replace parser architecture") {
		t.Fatalf("plan evidence before=%q after=%q", before, after)
	}
}

func TestRecoveryPlanTransitionIgnoresCompletedPriorPlan(t *testing.T) {
	a := &Agent{}
	a.setTodoState([]evidence.TodoItem{{Content: "Old task", Status: "completed"}})
	next := json.RawMessage(`{"todos":[{"content":"New user task","status":"in_progress"}]}`)
	if changed, _, _ := a.recoveryPlanTransition("todo_write", next); changed {
		t.Fatal("a new task after a completed plan is not a mid-plan transition")
	}
}

func (g *recordingRecoveryGate) ObserveResult(_ context.Context, observation RecoveryObservation) string {
	g.observation = observation
	return ""
}

func (*recordingRecoveryGate) BeforeMutation(context.Context, RecoveryProposal) (RecoveryDecision, error) {
	return RecoveryDecision{Allow: true}, nil
}

func TestObserveRecoveryResultMarksCancellation(t *testing.T) {
	gate := &recordingRecoveryGate{}
	a := &Agent{recoveryGate: gate}
	a.observeRecoveryResult(
		context.Background(),
		"write_file",
		json.RawMessage(`{"path":"a.go"}`),
		false,
		true,
		"cancelled",
		context.Canceled,
		false,
		false,
	)
	if !gate.observation.Cancelled {
		t.Fatalf("observation = %+v, want cancellation marked", gate.observation)
	}
}
