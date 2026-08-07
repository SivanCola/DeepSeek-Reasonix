package intent

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestCrossConsumerDisagreement pins the case that constrains the whole design.
//
// The golden corpus in internal/agent (goal_budget_class_test.go) requires one
// sentence to produce opposite answers for two consumers: a bare fault report
// starts a Goal on the write budget, while ordinary Delivery must not treat it
// as a mutation request. A flat boolean classifier cannot express this, which is
// why TurnIntent carries FaultReport separately from Kind.
func TestCrossConsumerDisagreement(t *testing.T) {
	// "数据模型管理器又出现历史 BUG 了……" - a malfunction reported with no
	// imperative verb and no explicit read-only constraint.
	bareFault := TurnIntent{Kind: KindObservableRead, FaultReport: true, Source: SourceClassifier}

	if bareFault.NeedsMutation() {
		t.Error("ordinary Delivery must not read a bare fault report as a mutation request")
	}
	if !bareFault.NeedsGoalWriteBudget() {
		t.Error("a Goal must start a bare fault report on the write budget")
	}
	if !bareFault.NeedsEvidence() {
		t.Error("a bare fault report is observable work and must arm the evidence gate")
	}
}

// TestReadOnlyConstraintOverridesMutationVerbs pins that an explicit prohibition
// beats any verb in the same turn. "review only and do not fix anything" carries
// the mutation verb "fix" but must never require a state change.
func TestReadOnlyConstraintOverridesMutationVerbs(t *testing.T) {
	constrained := TurnIntent{Kind: KindMutation, ReadOnlyConstraint: true, FaultReport: true}

	if constrained.NeedsMutation() {
		t.Error("an explicit read-only constraint must suppress mutation")
	}
	if constrained.NeedsPersistentAction() {
		t.Error("an explicit read-only constraint must suppress persistent writes")
	}
	if constrained.NeedsGoalWriteBudget() {
		t.Error("an explicit read-only constraint must keep a Goal off the write budget")
	}
	if !constrained.NeedsEvidence() {
		t.Error("review-only work is still observable work")
	}
}

// TestAdvisoryFaultStaysConsultative pins that a fault the user only wants
// explained ("why does this bug happen?") does not become write work.
func TestAdvisoryFaultStaysConsultative(t *testing.T) {
	advisory := TurnIntent{Kind: KindAdvisory, FaultReport: true}

	if advisory.NeedsGoalWriteBudget() {
		t.Error("an explained-only fault must stay on the simple Goal budget")
	}
	if advisory.NeedsEvidence() {
		t.Error("advisory questions must not arm the delivery evidence gate")
	}
}

// TestDurableScopeSeparatesMemoryKinds pins the distinction the corpus draws
// between "Remember ORBIT-42 and answer on the next turn" (conversation) and
// "Remember ORBIT-42 permanently across sessions" (persistent action).
func TestDurableScopeSeparatesMemoryKinds(t *testing.T) {
	conversational := TurnIntent{Kind: KindConversation, DurableScope: false}
	if conversational.NeedsPersistentAction() || conversational.NeedsEvidence() {
		t.Error("a next-turn memory request is conversation, not durable work")
	}

	durable := TurnIntent{Kind: KindPersistentAction, DurableScope: true}
	if !durable.NeedsPersistentAction() {
		t.Error("an across-sessions memory request must require a durable write")
	}
	if !durable.NeedsMutation() {
		t.Error("a durable write is a state change")
	}
	if !durable.NeedsEvidence() {
		t.Error("a durable write must arm the evidence gate")
	}
}

// TestUnknownIsNotConversation pins the distinction the package exists to keep:
// a turn nobody could read must not silently behave like chat. Consumers branch
// on Unknown and apply their own degraded default.
func TestUnknownIsNotConversation(t *testing.T) {
	var zero TurnIntent
	if !zero.Unknown() {
		t.Fatal("the zero TurnIntent must report Unknown")
	}
	if zero.NeedsEvidence() || zero.NeedsMutation() || zero.NeedsGoalWriteBudget() {
		t.Error("an unread turn must not arm any gate by default")
	}
}

// TestBoundedDegradesToUnknown pins the failure discipline: a classifier that
// errors, times out, or is absent yields Unknown rather than a reading, and
// never returns an error the caller could mistake for a classification.
func TestBoundedDegradesToUnknown(t *testing.T) {
	t.Run("nil inner", func(t *testing.T) {
		var b Bounded
		got, err := b.Classify(context.Background(), "fix the bug")
		if err != nil || !got.Unknown() {
			t.Fatalf("nil inner = (%v, %v), want (unknown, nil)", got.Kind, err)
		}
	})

	t.Run("inner error", func(t *testing.T) {
		audit := &Audit{}
		b := Bounded{
			Inner: ClassifierFunc(func(context.Context, string) (TurnIntent, error) {
				return TurnIntent{Kind: KindMutation}, errors.New("provider down")
			}),
			Audit: audit,
		}
		got, err := b.Classify(context.Background(), "fix the bug")
		if err != nil {
			t.Fatalf("Classify returned an error: %v", err)
		}
		if !got.Unknown() {
			t.Fatalf("a failed classification leaked Kind %v", got.Kind)
		}
		if snap := audit.Snapshot(); snap["degraded"] != 1 {
			t.Errorf("degraded counter = %d, want 1", snap["degraded"])
		}
	})

	t.Run("timeout is bounded", func(t *testing.T) {
		b := Bounded{
			Timeout: 10 * time.Millisecond,
			Inner: ClassifierFunc(func(ctx context.Context, _ string) (TurnIntent, error) {
				<-ctx.Done()
				return TurnIntent{}, ctx.Err()
			}),
		}
		start := time.Now()
		got, err := b.Classify(context.Background(), "fix the bug")
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("Classify took %v; the timeout did not bound it", elapsed)
		}
		if err != nil || !got.Unknown() {
			t.Fatalf("timed-out classify = (%v, %v), want (unknown, nil)", got.Kind, err)
		}
	})
}

// TestBoundedCachesAnswers pins that a repeated turn does not re-call the model,
// and that the cache expires.
func TestBoundedCachesAnswers(t *testing.T) {
	calls := 0
	now := time.Unix(0, 0)
	audit := &Audit{}
	b := Bounded{
		TTL:   time.Minute,
		Audit: audit,
		Now:   func() time.Time { return now },
		Inner: ClassifierFunc(func(context.Context, string) (TurnIntent, error) {
			calls++
			return TurnIntent{Kind: KindMutation}, nil
		}),
	}

	for i := 0; i < 3; i++ {
		got, _ := b.Classify(context.Background(), "fix the bug")
		if got.Kind != KindMutation {
			t.Fatalf("call %d = %v, want mutation", i, got.Kind)
		}
		if got.Source != SourceClassifier {
			t.Errorf("call %d source = %v, want classifier", i, got.Source)
		}
	}
	if calls != 1 {
		t.Errorf("inner called %d times, want 1 (cache should absorb repeats)", calls)
	}
	if snap := audit.Snapshot(); snap["cache_hits"] != 2 {
		t.Errorf("cache_hits = %d, want 2", snap["cache_hits"])
	}

	now = now.Add(2 * time.Minute)
	if _, err := b.Classify(context.Background(), "fix the bug"); err != nil {
		t.Fatalf("post-expiry classify: %v", err)
	}
	if calls != 2 {
		t.Errorf("inner called %d times after expiry, want 2", calls)
	}
}
