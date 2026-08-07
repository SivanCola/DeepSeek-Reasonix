package agent

import (
	"context"
	"strings"
	"sync"
	"time"

	"reasonix/internal/intent"
	"reasonix/internal/taskintent"
)

// turnIntentAwaitBudget bounds how long the gate waits for a classification that
// was launched at turn start. It is short on purpose: the classification runs
// concurrently with the model's own work, so by gate time it has almost always
// landed, and a turn that finishes faster than the classifier should degrade to
// unknown rather than stall on it.
const turnIntentAwaitBudget = time.Second

// turnIntentFuture carries a classification launched at turn start so the gate
// can consume the result without paying the model latency inline.
//
// The result is written before done is closed and read only after, so the
// channel close is the happens-before edge; the timeout path touches nothing.
// A fresh future per turn is what keeps a slow classification from ever landing
// on a later turn: the stale goroutine completes into an object nobody reads.
type turnIntentFuture struct {
	done   chan struct{}
	result intent.TurnIntent
}

// await returns the classification and whether it landed at all. The second
// return separates "the classifier read the turn as nothing" from "the
// classification was still in flight", which are different defects: the first
// is a model problem, the second means the await budget is too small for the
// observed turn durations.
func (f *turnIntentFuture) await(budget time.Duration) (intent.TurnIntent, bool) {
	if f == nil {
		return intent.TurnIntent{}, false
	}
	// Fast path: already landed, no timer.
	select {
	case <-f.done:
		return f.result, true
	default:
	}
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-f.done:
		return f.result, true
	case <-timer.C:
		return intent.TurnIntent{}, false
	}
}

// startTurnIntentClassification launches tier 3 concurrently with the turn.
//
// Running it here rather than at the gate is the whole point: a classifier
// measured at ~3.8s would be that much added latency if it ran when the model
// wants to finish, but overlapped with the turn's own model round-trips it is
// effectively free. The cost is that a turn later answered by the declared tier
// still paid for a classification it did not use.
//
// The injected Classifier must bound itself - intent.Bounded provides the
// timeout, cache, and degradation. An unbounded classifier would leak this
// goroutine for as long as it hangs.
func (a *Agent) startTurnIntentClassification(ctx context.Context, text string) *turnIntentFuture {
	if a == nil || a.intentClassifier == nil || strings.TrimSpace(text) == "" {
		return nil
	}
	// Plan mode is answered by tier 1, so tier 3 would never be consulted.
	// Skipping the call here is the difference between paying for it and not.
	if a.planMode.Load() {
		return nil
	}
	// Capture the classifier before launching. The goroutine must not reach back
	// into Agent fields: it outlives this call, so any concurrent write to the
	// field would race, and capturing also pins the turn to the classifier that
	// was configured when it started.
	cls := a.intentClassifier
	f := &turnIntentFuture{done: make(chan struct{})}
	go func() {
		defer close(f.done)
		got, err := cls.Classify(ctx, text)
		if err != nil {
			// Leaves the zero value, which reads as unknown. A failed
			// classification must never look like a reading of the turn.
			return
		}
		f.result = got
	}()
	return f
}

// deliveryExpectations is the gate-facing triple: the three predictions
// finalReadinessCheckFor consults when it decides whether a turn is allowed to
// finish. It stays a local type because it is host state, not a reading of the
// user - deriving it from a reading is what deliveryExpectationsFrom does.
type deliveryExpectations struct {
	Task       bool
	Mutation   bool
	Persistent bool
}

// deliveryExpectationsFrom derives the gate triple from a shared TurnIntent.
//
// The writerTools guard stays here rather than in the contract on purpose:
// TurnIntent says what the user asked for, while whether this agent can perform
// a mutation at all is host capability. A registry with no writers can never
// satisfy a "state change required" expectation, so arming it would deadlock the
// turn.
//
// Mutation additionally narrows to KindMutation. intent.NeedsMutation also
// answers true for KindPersistentAction - correct for the Goal budget, which
// asks "does this turn write anything" - but the delivery gate keeps durable
// memory on its own receipt contract (see the persistentOnlyReady branch in
// finalReadinessCheckFor) and must not demand a code mutation for it.
func deliveryExpectationsFrom(ti intent.TurnIntent, writerTools bool) deliveryExpectations {
	return deliveryExpectations{
		Task:       ti.NeedsEvidence(),
		Mutation:   ti.NeedsMutation() && ti.Kind == intent.KindMutation && writerTools,
		Persistent: ti.NeedsPersistentAction(),
	}
}

// legacyTurnIntent expresses the keyword classifier as a TurnIntent so the old
// and new paths speak one vocabulary during the shadow phase.
//
// Kind maps one-to-one from taskintent's enum. The extra contract fields are
// left at their zero values deliberately: the keyword tables never produced
// them as separate facts, they folded negation and diagnosis directly into the
// classification, and inventing values here would misreport what the legacy
// path actually knows.
func legacyTurnIntent(input string) intent.TurnIntent {
	kind := intent.KindUnknown
	switch taskintent.Classify(input) {
	case taskintent.Conversation:
		kind = intent.KindConversation
	case taskintent.Advisory:
		kind = intent.KindAdvisory
	case taskintent.ObservableRead:
		kind = intent.KindObservableRead
	case taskintent.Mutation:
		kind = intent.KindMutation
	case taskintent.PersistentAction:
		kind = intent.KindPersistentAction
	}
	return intent.TurnIntent{Kind: kind, Source: intent.SourceKeyword}
}

// legacyDeliveryExpectations reproduces the historical turn-start classification
// exactly. Routing every caller through one function keeps the shadow comparison
// to a single call and leaves the switch-over exactly one site to delete.
//
// Persistent is computed by the direct legacy call rather than derived from
// Kind. The two disagree by design: taskintent.Classify tests mutation before
// persistence, so a turn that asks for both reports Mutation while
// taskintent.NeedsPersistentAction still reports true. Persistence is an
// orthogonal axis in the legacy, and reproducing it faithfully is the point of
// this function.
func legacyDeliveryExpectations(input string, writerTools bool) deliveryExpectations {
	out := deliveryExpectationsFrom(legacyTurnIntent(input), writerTools)
	out.Persistent = taskintent.NeedsPersistentAction(input)
	return out
}

// resolveTurnIntent answers the gate's question from the two zero-cost sources
// that outrank prose classification, returning the shared contract type.
//
// It must run at gate time rather than at turn start. beginRunTurn resets the
// evidence ledger before classifying, so at turn start the model has declared
// nothing; by the time finalReadinessCheckFor runs, the turn's receipts exist
// and the declared tier can speak.
//
// An unread turn comes back as KindUnknown with SourceNone. The caller owns the
// degraded-mode default; this function never guesses, and KindUnknown must not
// be treated as KindConversation.
func (a *Agent) resolveTurnIntent() intent.TurnIntent {
	if a == nil {
		return intent.TurnIntent{}
	}

	// Tier 1 - host state. A plan-mode turn is a planning turn by host decree:
	// it is not held to host-observable work, and holding it there would fight
	// the plan-mode contract itself.
	if a.planMode.Load() {
		return intent.TurnIntent{Kind: intent.KindConversation, Source: intent.SourceExplicit}
	}

	if a.evidence == nil {
		return intent.TurnIntent{}
	}

	// Tier 2 - the model's own declarations, read from typed receipts.
	//
	// Writing todos or acceptance criteria is the model stating that this turn is
	// a task with a completion contract. It is the same signal beginRunTurn
	// already trusts for deliveryCriteriaEstablished, consulted here for the
	// expectation it was never wired to.
	//
	// It reports KindObservableRead and nothing stronger. An earlier version also
	// inferred KindMutation from an unfinished todo list plus writer tools; real
	// shadow traffic disproved it on "帮我看下 main.go 有没有问题，只分析不要改",
	// where the model wrote todos for an explicitly read-only analysis and the
	// inference armed a mutation expectation the user had forbidden. An unfinished
	// todo list proves the model thinks the work is incomplete - it does not prove
	// a state change was requested, and only the user's own words do.
	//
	// The consequence is a real gap: tier 2 can establish that a turn is a task
	// but never that it requires a mutation, so a todo-writing turn stops the
	// cascade before tier 3 could answer that question. Resolving it needs
	// per-field tier composition rather than one winning tier; see the plan.
	if a.evidence.HasSuccessfulTodoWrite() || a.evidence.HasSuccessfulAcceptanceCriteria() {
		return intent.TurnIntent{Kind: intent.KindObservableRead, Source: intent.SourceDeclared}
	}

	// A completed remember receipt is deliberately NOT read as evidence of a
	// persistent-action turn. Observing that the model saved something proves it
	// happened, not that the user required it, and treating the two as the same
	// would let the gate justify itself with its own output.

	// Tier 3 - the classification launched at turn start. Consulted last because
	// it is the only tier that interprets prose, and the only one that can be
	// wrong in ways the cheaper tiers cannot.
	got, landed := a.turnIntentPending.await(turnIntentAwaitBudget)
	if !landed && a.turnIntentPending != nil {
		deliveryIntentShadow.recordAwaitTimeout()
	}
	if !got.Unknown() {
		got.Source = intent.SourceClassifier
		return got
	}

	return intent.TurnIntent{}
}

// resolveDeliveryExpectations resolves the gate triple through the shared
// contract. The second return reports which tier answered so the shadow audit
// can measure how often the zero-cost tiers suffice - the number that decides
// whether a model classifier is affordable on this path.
func (a *Agent) resolveDeliveryExpectations() (deliveryExpectations, intent.Source) {
	ti := a.resolveTurnIntent()
	if ti.Unknown() {
		return deliveryExpectations{}, ti.Source
	}
	return deliveryExpectationsFrom(ti, registryHasWriterTools(a.tools)), ti.Source
}

// deliveryIntentShadowAudit accumulates agreement counters between the legacy
// keyword classifier and the tiered resolver. It is process-wide and
// non-persisted, the same shape capability.Audit uses, so a corpus run can
// report totals without threading a sink through every Agent construction.
type deliveryIntentShadowAudit struct {
	mu sync.Mutex

	Turns              int
	Agree              int
	Disagree           int
	ResolvedHost       int
	ResolvedModel      int
	ResolvedClassifier int
	AwaitTimeouts      int
	Unresolved         int
	TaskDiff           int
	MutationDiff       int
	PersistentDiff     int
}

var deliveryIntentShadow deliveryIntentShadowAudit

// recordAwaitTimeout notes that a classification had not landed when the gate
// needed it. A nonzero count means turnIntentAwaitBudget is too small for the
// observed turn durations, not that the classifier is inaccurate.
func (s *deliveryIntentShadowAudit) recordAwaitTimeout() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.AwaitTimeouts++
}

// record captures one gate-time comparison. legacy is the value that actually
// drove behavior; next is what the tiered resolver would have produced.
func (s *deliveryIntentShadowAudit) record(legacy, next deliveryExpectations, source intent.Source) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Turns++
	switch source {
	case intent.SourceExplicit:
		s.ResolvedHost++
	case intent.SourceDeclared:
		s.ResolvedModel++
	case intent.SourceClassifier:
		s.ResolvedClassifier++
	default:
		s.Unresolved++
	}

	if legacy == next {
		s.Agree++
		return
	}
	s.Disagree++
	if legacy.Task != next.Task {
		s.TaskDiff++
	}
	if legacy.Mutation != next.Mutation {
		s.MutationDiff++
	}
	if legacy.Persistent != next.Persistent {
		s.PersistentDiff++
	}
}

// DeliveryIntentShadowSnapshot returns a copy of the shadow counters. Exported
// for the differential adjudication run, which needs aggregate agreement numbers
// across a whole test binary.
func DeliveryIntentShadowSnapshot() map[string]int {
	deliveryIntentShadow.mu.Lock()
	defer deliveryIntentShadow.mu.Unlock()
	return map[string]int{
		"turns":               deliveryIntentShadow.Turns,
		"agree":               deliveryIntentShadow.Agree,
		"disagree":            deliveryIntentShadow.Disagree,
		"resolved_host":       deliveryIntentShadow.ResolvedHost,
		"resolved_model":      deliveryIntentShadow.ResolvedModel,
		"resolved_classifier": deliveryIntentShadow.ResolvedClassifier,
		"await_timeouts":      deliveryIntentShadow.AwaitTimeouts,
		"unresolved":          deliveryIntentShadow.Unresolved,
		"task_diff":           deliveryIntentShadow.TaskDiff,
		"mutation_diff":       deliveryIntentShadow.MutationDiff,
		"persistent_diff":     deliveryIntentShadow.PersistentDiff,
	}
}

// ResetDeliveryIntentShadow clears the counters so a corpus run can scope its
// measurement to one batch. Fields are cleared individually because assigning a
// zero struct over the audit would copy its mutex.
func ResetDeliveryIntentShadow() {
	deliveryIntentShadow.mu.Lock()
	defer deliveryIntentShadow.mu.Unlock()
	deliveryIntentShadow.Turns = 0
	deliveryIntentShadow.Agree = 0
	deliveryIntentShadow.Disagree = 0
	deliveryIntentShadow.ResolvedHost = 0
	deliveryIntentShadow.ResolvedModel = 0
	deliveryIntentShadow.ResolvedClassifier = 0
	deliveryIntentShadow.AwaitTimeouts = 0
	deliveryIntentShadow.Unresolved = 0
	deliveryIntentShadow.TaskDiff = 0
	deliveryIntentShadow.MutationDiff = 0
	deliveryIntentShadow.PersistentDiff = 0
}
