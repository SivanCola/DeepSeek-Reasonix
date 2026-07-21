package recovery

// Route is the pure decision outcome for a proposed action.
type Route int

const (
	// RouteBypass leaves the call to the ordinary Ask/YOLO approval path.
	RouteBypass Route = iota
	// RouteAllow lets Auto execute without a human card or reviewer call.
	RouteAllow
	// RouteReview hands an ambiguous recovery mutation to the isolated reviewer.
	RouteReview
	// RouteAsk shows a single human confirmation card.
	RouteAsk
)

// String returns a stable route name for tests and diagnostics.
func (r Route) String() string {
	switch r {
	case RouteBypass:
		return "bypass"
	case RouteAllow:
		return "allow"
	case RouteReview:
		return "review"
	case RouteAsk:
		return "ask"
	default:
		return "unknown"
	}
}

// AskReason is the rule-derived reason a card is shown before any reviewer call.
type AskReason string

const (
	AskNone     AskReason = ""
	AskRisk     AskReason = "risk"
	AskScope    AskReason = "scope"
	AskStrategy AskReason = "strategy"
	AskRepeat   AskReason = "repeat"
)

// Facts are the host-observed inputs for the pure decision engine.
// The engine never locks, calls a model, shows UI, or mutates state.
type Facts struct {
	// AutoMode is true only when tool-approval mode is Auto.
	AutoMode bool

	// Proposal classification.
	ReadOnly     bool
	Mutates      bool
	Verification bool
	HighRisk     bool

	// Active failure context (zero values when none).
	HasActiveFailure   bool
	ExpandedScope      bool
	StrategyChanged    bool
	SafeRetryAvailable bool
	FailureCount       uint8 // 1 = first failure; 2+ = second failure in recovery
}

// Decision is the pure routing result.
type DecisionResult struct {
	Route     Route
	AskReason AskReason
	// ConsumeSafeRetry is set when RouteAllow was chosen because this is the
	// first safe verification retry; the coordinator must spend the budget.
	ConsumeSafeRetry bool
}

// Decide is the pure Auto Guard decision engine.
//
// Order is fixed by product policy:
//  1. non-Auto → bypass ordinary approval
//  2. read-only diagnosis → allow
//  3. deterministic high risk → ask
//  4. no active failure → allow ordinary mutations
//  5. first safe verification retry → allow (+ consume budget)
//  6. expanded scope / method change / second failure → ask
//  7. remaining failure-recovery mutations → reviewer
func Decide(f Facts) DecisionResult {
	if !f.AutoMode {
		return DecisionResult{Route: RouteBypass}
	}
	// Non-mutating, non-verification calls (and host-proven read-only tools)
	// always continue so diagnosis can proceed without cards.
	if f.ReadOnly && !f.Mutates {
		return DecisionResult{Route: RouteAllow}
	}
	if !f.Mutates && !f.Verification {
		return DecisionResult{Route: RouteAllow}
	}
	if f.HighRisk {
		return DecisionResult{Route: RouteAsk, AskReason: AskRisk}
	}
	if !f.HasActiveFailure {
		return DecisionResult{Route: RouteAllow}
	}
	if f.SafeRetryAvailable {
		return DecisionResult{Route: RouteAllow, ConsumeSafeRetry: true}
	}
	if f.ExpandedScope {
		return DecisionResult{Route: RouteAsk, AskReason: AskScope}
	}
	if f.StrategyChanged {
		return DecisionResult{Route: RouteAsk, AskReason: AskStrategy}
	}
	// A second qualifying failure during the same recovery process needs a human.
	if f.FailureCount >= 2 {
		return DecisionResult{Route: RouteAsk, AskReason: AskRepeat}
	}
	return DecisionResult{Route: RouteReview}
}

// ChangeKindForAsk maps a rule-derived ask reason onto the wire ChangeKind.
func ChangeKindForAsk(reason AskReason) ChangeKind {
	switch reason {
	case AskRisk, AskRepeat:
		return ChangeRisk
	case AskScope:
		return ChangeScope
	case AskStrategy:
		return ChangeStrategy
	default:
		return ChangeUncertain
	}
}
