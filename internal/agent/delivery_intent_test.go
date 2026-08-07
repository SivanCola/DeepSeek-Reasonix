package agent

import (
	"testing"

	"reasonix/internal/evidence"
	"reasonix/internal/intent"
	"reasonix/internal/intent/corpus"
	"reasonix/internal/taskintent"
)

// TestLegacyDeliveryExpectationsMatchesHistoricalExpression is the guard for the
// shadow refactor. legacyDeliveryExpectations now routes through the shared
// intent contract, and this pins that the rewrite is behavior-preserving: for
// every corpus turn, under both writer-tool states, it must equal the exact
// expression beginRunTurn used before the contract existed.
func TestLegacyDeliveryExpectationsMatchesHistoricalExpression(t *testing.T) {
	for _, c := range corpus.All() {
		for _, writers := range []bool{false, true} {
			kind := taskintent.Classify(c.Text)
			want := deliveryExpectations{
				Task: kind == taskintent.ObservableRead ||
					kind == taskintent.Mutation ||
					kind == taskintent.PersistentAction,
				Mutation:   kind == taskintent.Mutation && writers,
				Persistent: taskintent.NeedsPersistentAction(c.Text),
			}
			if got := legacyDeliveryExpectations(c.Text, writers); got != want {
				t.Errorf("legacyDeliveryExpectations(%q, writers=%v) = %+v, want %+v",
					c.Text, writers, got, want)
			}
		}
	}
}

// TestDeliveryExpectationsFromContract pins the two places the gate triple
// deliberately narrows the shared contract.
func TestDeliveryExpectationsFromContract(t *testing.T) {
	t.Run("mutation needs writer tools", func(t *testing.T) {
		ti := intent.TurnIntent{Kind: intent.KindMutation}
		if got := deliveryExpectationsFrom(ti, false); got.Mutation {
			t.Error("armed a mutation expectation with no writer tools; the turn could never satisfy it")
		}
		if got := deliveryExpectationsFrom(ti, true); !got.Mutation {
			t.Error("writer tools present but mutation expectation not armed")
		}
	})

	t.Run("durable memory does not demand a code mutation", func(t *testing.T) {
		ti := intent.TurnIntent{Kind: intent.KindPersistentAction, DurableScope: true}
		got := deliveryExpectationsFrom(ti, true)
		if got.Mutation {
			t.Error("a durable-memory turn must not arm the code-mutation expectation")
		}
		if !got.Persistent {
			t.Error("a durable-memory turn must arm the persistent expectation")
		}
		if !got.Task {
			t.Error("a durable-memory turn is observable work")
		}
	})

	t.Run("read-only constraint suppresses mutation", func(t *testing.T) {
		ti := intent.TurnIntent{Kind: intent.KindMutation, ReadOnlyConstraint: true}
		if got := deliveryExpectationsFrom(ti, true); got.Mutation {
			t.Error("an explicit read-only constraint must suppress the mutation expectation")
		}
	})
}

// TestResolveTurnIntentTiers pins which source answers, and that an unread turn
// is reported as unknown rather than silently becoming conversation.
func TestResolveTurnIntentTiers(t *testing.T) {
	t.Run("nil agent is unknown", func(t *testing.T) {
		var a *Agent
		if got := a.resolveTurnIntent(); !got.Unknown() {
			t.Errorf("nil agent resolved to %v", got.Kind)
		}
	})

	t.Run("plan mode is host state", func(t *testing.T) {
		a := &Agent{}
		a.planMode.Store(true)
		got := a.resolveTurnIntent()
		if got.Source != intent.SourceExplicit {
			t.Errorf("source = %v, want explicit", got.Source)
		}
		if got.NeedsEvidence() {
			t.Error("a plan-mode turn must not be held to host-observable work")
		}
	})

	t.Run("silent turn is unresolved, not conversation", func(t *testing.T) {
		a := &Agent{evidence: evidence.NewLedger()}
		got := a.resolveTurnIntent()
		if !got.Unknown() {
			t.Fatalf("a turn with no receipts resolved to %v; it must stay unknown", got.Kind)
		}
		if got.Source != intent.SourceNone {
			t.Errorf("source = %v, want none", got.Source)
		}
		// The distinction matters: an unresolved turn must not arm gates by
		// default, but it must also not be mistaken for a deliberate reading.
		exp, src := a.resolveDeliveryExpectations()
		if exp != (deliveryExpectations{}) {
			t.Errorf("unresolved turn armed %+v", exp)
		}
		if src != intent.SourceNone {
			t.Errorf("source = %v, want none", src)
		}
	})
}

// TestShadowAuditCountsBySource pins that the audit separates the zero-cost
// tiers from the unresolved remainder. That split is the number which decides
// whether a model classifier is affordable on this path.
func TestShadowAuditCountsBySource(t *testing.T) {
	ResetDeliveryIntentShadow()
	t.Cleanup(ResetDeliveryIntentShadow)

	same := deliveryExpectations{Task: true}
	deliveryIntentShadow.record(same, same, intent.SourceExplicit)
	deliveryIntentShadow.record(same, same, intent.SourceDeclared)
	deliveryIntentShadow.record(same, deliveryExpectations{}, intent.SourceNone)

	snap := DeliveryIntentShadowSnapshot()
	for key, want := range map[string]int{
		"turns": 3, "agree": 2, "disagree": 1,
		"resolved_host": 1, "resolved_model": 1, "unresolved": 1,
		"task_diff": 1, "mutation_diff": 0, "persistent_diff": 0,
	} {
		if snap[key] != want {
			t.Errorf("%s = %d, want %d", key, snap[key], want)
		}
	}
}
