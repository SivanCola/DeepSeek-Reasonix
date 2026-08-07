package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/evidence"
	"reasonix/internal/intent"
)

func stubClassifier(ti intent.TurnIntent, calls *atomic.Int64) intent.Classifier {
	return intent.ClassifierFunc(func(context.Context, string) (intent.TurnIntent, error) {
		if calls != nil {
			calls.Add(1)
		}
		return ti, nil
	})
}

// agentWithTier3 builds the minimum Agent the resolver reads, then launches the
// tier-3 future the way beginRunTurn does.
func agentWithTier3(t *testing.T, cls intent.Classifier) *Agent {
	t.Helper()
	a := &Agent{evidence: evidence.NewLedger(), intentClassifier: cls}
	a.turnIntentPending = a.startTurnIntentClassification(context.Background(), "fix the parser")
	return a
}

// TestTier3AnswersOnlyWhenCheaperTiersAreSilent pins the ordering that makes the
// design affordable: the classifier is the fallback, never the first resort.
func TestTier3AnswersOnlyWhenCheaperTiersAreSilent(t *testing.T) {
	t.Run("silent turn falls through to the classifier", func(t *testing.T) {
		var calls atomic.Int64
		a := agentWithTier3(t, stubClassifier(intent.TurnIntent{Kind: intent.KindMutation}, &calls))

		got := a.resolveTurnIntent()
		if got.Kind != intent.KindMutation {
			t.Fatalf("kind = %v, want mutation", got.Kind)
		}
		if got.Source != intent.SourceClassifier {
			t.Errorf("source = %v, want classifier", got.Source)
		}
		if calls.Load() != 1 {
			t.Errorf("classifier called %d times, want 1", calls.Load())
		}
	})

	t.Run("declared tier outranks the classifier", func(t *testing.T) {
		var calls atomic.Int64
		a := agentWithTier3(t, stubClassifier(intent.TurnIntent{Kind: intent.KindConversation}, &calls))
		// The model declared a task by writing todos; that must win over any
		// reading of the user's prose.
		a.evidence.Record(evidence.Receipt{ToolName: "todo_write", Success: true})

		got := a.resolveTurnIntent()
		if got.Source != intent.SourceDeclared {
			t.Fatalf("source = %v, want declared", got.Source)
		}
		if !got.NeedsEvidence() {
			t.Error("a declared task must arm the evidence expectation")
		}
	})

	t.Run("plan mode skips the classifier entirely", func(t *testing.T) {
		var calls atomic.Int64
		a := &Agent{evidence: evidence.NewLedger(), intentClassifier: stubClassifier(
			intent.TurnIntent{Kind: intent.KindMutation}, &calls)}
		a.planMode.Store(true)
		a.turnIntentPending = a.startTurnIntentClassification(context.Background(), "fix the parser")

		got := a.resolveTurnIntent()
		if got.Source != intent.SourceExplicit {
			t.Errorf("source = %v, want explicit", got.Source)
		}
		if calls.Load() != 0 {
			t.Errorf("classifier called %d times in plan mode; tier 1 already answers", calls.Load())
		}
		if a.turnIntentPending != nil {
			t.Error("plan mode must not launch a classification")
		}
	})
}

// TestTier3FailureDegradesToUnknown pins that a classifier outage cannot look
// like a reading of the turn. Unknown must not arm gates, and must not be
// mistaken for a deliberate "this is chat" verdict.
func TestTier3FailureDegradesToUnknown(t *testing.T) {
	cases := map[string]intent.Classifier{
		"error": intent.ClassifierFunc(func(context.Context, string) (intent.TurnIntent, error) {
			return intent.TurnIntent{Kind: intent.KindMutation}, errors.New("provider down")
		}),
		"unknown reply": intent.ClassifierFunc(func(context.Context, string) (intent.TurnIntent, error) {
			return intent.TurnIntent{}, nil
		}),
	}
	for name, cls := range cases {
		t.Run(name, func(t *testing.T) {
			a := agentWithTier3(t, cls)
			got := a.resolveTurnIntent()
			if !got.Unknown() {
				t.Fatalf("degraded classification leaked kind %v", got.Kind)
			}
			exp, src := a.resolveDeliveryExpectations()
			if exp != (deliveryExpectations{}) {
				t.Errorf("degraded turn armed %+v", exp)
			}
			if src != intent.SourceNone {
				t.Errorf("source = %v, want none", src)
			}
		})
	}
}

// TestTier3AwaitIsBounded pins that a classification still in flight cannot
// stall turn completion past the await budget.
func TestTier3AwaitIsBounded(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	f := &turnIntentFuture{done: make(chan struct{})}
	go func() {
		<-release
		close(f.done)
	}()

	start := time.Now()
	got, landed := f.await(50 * time.Millisecond)
	elapsed := time.Since(start)

	if !got.Unknown() {
		t.Errorf("an unfinished classification returned %v", got.Kind)
	}
	if landed {
		t.Error("await reported the classification landed when it had not")
	}
	if elapsed > time.Second {
		t.Errorf("await took %v; the budget did not bound it", elapsed)
	}
}

// TestTier3FutureIsPerTurn pins that a slow classification cannot land on a
// later turn. beginRunTurn replaces the future every turn, so the stale
// goroutine completes into an object nobody reads.
func TestTier3FutureIsPerTurn(t *testing.T) {
	slow := make(chan struct{})
	t.Cleanup(func() { close(slow) })

	a := &Agent{evidence: evidence.NewLedger()}
	a.intentClassifier = intent.ClassifierFunc(func(context.Context, string) (intent.TurnIntent, error) {
		<-slow
		return intent.TurnIntent{Kind: intent.KindMutation}, nil
	})

	// Turn 1 launches a classification that never lands in time.
	first := a.startTurnIntentClassification(context.Background(), "turn one")
	if got, _ := first.await(20 * time.Millisecond); !got.Unknown() {
		t.Fatalf("slow classification resolved early: %v", got.Kind)
	}

	// Turn 2 replaces it with a fast one; turn 1's result must be unreachable.
	a.intentClassifier = stubClassifier(intent.TurnIntent{Kind: intent.KindAdvisory}, nil)
	a.turnIntentPending = a.startTurnIntentClassification(context.Background(), "turn two")

	got := a.resolveTurnIntent()
	if got.Kind != intent.KindAdvisory {
		t.Errorf("kind = %v, want advisory from the current turn", got.Kind)
	}
	if a.turnIntentPending == first {
		t.Error("the turn-1 future was reused")
	}
}

// TestTier3AwaitIsRepeatable pins that the gate may run more than once per turn
// (run_loop and the readiness recheck both call it) without losing the answer.
func TestTier3AwaitIsRepeatable(t *testing.T) {
	var calls atomic.Int64
	a := agentWithTier3(t, stubClassifier(intent.TurnIntent{Kind: intent.KindMutation}, &calls))

	for i := 0; i < 3; i++ {
		got := a.resolveTurnIntent()
		if got.Kind != intent.KindMutation {
			t.Fatalf("resolve %d = %v, want mutation", i, got.Kind)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("classifier called %d times across 3 resolves, want 1", calls.Load())
	}
}
