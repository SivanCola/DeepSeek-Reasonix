// Package intent carries one structured reading of a user turn, shared by every
// host-side gate that needs to know what the user asked for.
//
// It exists because the same question was previously answered independently in
// several places - the delivery evidence gate, the Goal budget classifier, and
// the planner route gate each kept their own keyword tables. Those tables drifted
// (five separate mutation-verb lists) and disagreed (two different negation word
// lists), so the same sentence could be judged differently by two gates in one
// turn.
//
// The split of responsibility here is deliberate:
//
//   - Perception (what did the user ask for?) is one result, produced once.
//   - Derivation (what does that mean for my gate?) stays deterministic Go,
//     lives next to the fields it reads, and is unit-testable without a model.
//
// Keeping derivation deterministic is not a stylistic preference. The golden
// corpus pins a case where one sentence must produce opposite answers for two
// consumers: a bare fault report such as "数据模型管理器又出现历史 BUG 了……"
// must start a Goal on the write budget while ordinary Delivery must not treat it
// as a mutation request. A single boolean classifier cannot express that; a
// structured intent plus per-consumer derivation can.
package intent

// Kind is the coarse reading of a turn, ordered from least to most host-visible
// work. It mirrors the classification the delivery gate has always made, lifted
// out of internal/agent so other gates can share one vocabulary.
type Kind uint8

const (
	// KindUnknown means no source could read the turn. Consumers must apply
	// their own degraded-mode default rather than treating it as any other Kind;
	// inventing an answer here is the defect this package exists to remove.
	KindUnknown Kind = iota
	// KindConversation is chat: greetings, acknowledgements, and requests whose
	// whole scope is the current conversation.
	KindConversation
	// KindAdvisory is a question seeking explanation or recommendation rather
	// than host-observable work.
	KindAdvisory
	// KindObservableRead requires the host to look at real state - read files,
	// run read-only commands, inspect a repository - but not change it.
	KindObservableRead
	// KindMutation requires a state change the host can observe.
	KindMutation
	// KindPersistentAction requires a durable write that outlives the session,
	// such as saving a memory across sessions.
	KindPersistentAction
)

func (k Kind) String() string {
	switch k {
	case KindConversation:
		return "conversation"
	case KindAdvisory:
		return "advisory"
	case KindObservableRead:
		return "observable_read"
	case KindMutation:
		return "mutation"
	case KindPersistentAction:
		return "persistent_action"
	default:
		return "unknown"
	}
}

// Source names which tier produced a TurnIntent, ordered by trust. It is carried
// on the result so audits can report how often the expensive tier was needed and
// so consumers can choose to distrust a low-confidence classification.
type Source uint8

const (
	// SourceNone accompanies KindUnknown.
	SourceNone Source = iota
	// SourceExplicit came from syntax the user typed with an agreed meaning, or
	// from a host toggle the user set. Never inferred from prose.
	SourceExplicit
	// SourceDeclared came from the model's own structured output - typed tool
	// arguments and evidence receipts it already committed to.
	SourceDeclared
	// SourceKeyword came from the legacy substring tables. It is a prose reading
	// like SourceClassifier, but kept distinct so migration audits can tell an
	// answer the word tables produced from one a model produced.
	SourceKeyword
	// SourceClassifier came from reading the user's prose with a model.
	SourceClassifier
)

func (s Source) String() string {
	switch s {
	case SourceExplicit:
		return "explicit"
	case SourceDeclared:
		return "declared"
	case SourceKeyword:
		return "keyword"
	case SourceClassifier:
		return "classifier"
	default:
		return "none"
	}
}

// TurnIntent is the shared structured reading of one user turn.
//
// Fields beyond Kind exist because the corpus proves Kind alone is lossy: two
// consumers need to disagree about the same turn, and they can only do that if
// the facts that distinguish them survive into the result.
type TurnIntent struct {
	Kind Kind

	// FaultReport is set when the user described something malfunctioning,
	// whether or not they asked for a repair - "the auth isn't working",
	// "页面卡住了". It is the field that lets a Goal treat a bare fault as write
	// work while Delivery still treats it as a read.
	FaultReport bool

	// ReadOnlyConstraint is set when the user explicitly forbade changes:
	// "review only and do not fix anything", "只分析原因，不要修改代码". It must
	// suppress write inference even when mutation verbs are present, so it is
	// carried separately rather than folded into Kind.
	ReadOnlyConstraint bool

	// DiagnosticIntent is set when the user asked to understand a fault rather
	// than repair it - "diagnose", "identify the root cause", "reproduce",
	// "诊断", "定位原因". It is deliberately separate from ReadOnlyConstraint:
	// that field is an explicit prohibition ("do not change anything"), while
	// this one is a scope the user chose without forbidding anything. Live
	// evaluation found the distinction is load-bearing - "诊断数据库连接失败原因。"
	// carries no prohibition, yet must keep a Goal off the write budget.
	DiagnosticIntent bool

	// DurableScope is set when the request reaches beyond this session -
	// "permanently", "across sessions", "跨会话". Paired with a save/remember
	// request it is what separates KindPersistentAction from a conversational
	// "remember this for the next turn".
	DurableScope bool

	// Source records which tier answered.
	Source Source
}

// Unknown reports whether no tier could read the turn. Consumers must branch on
// this before using any derivation: the derivations below answer for a turn that
// was actually read, and a zero TurnIntent is not a conversation.
func (t TurnIntent) Unknown() bool { return t.Kind == KindUnknown }

// IsTask reports whether the turn reads as actionable work rather than chat.
// Greetings and acknowledgements must not arm the delivery gates; everything
// that asks for something - including a question about real state - does.
func (t TurnIntent) IsTask() bool {
	return t.Kind != KindConversation && t.Kind != KindUnknown
}

// NeedsEvidence reports whether the turn must show host-observable work before
// it may finish. Advisory questions and chat must not arm the gate.
func (t TurnIntent) NeedsEvidence() bool {
	switch t.Kind {
	case KindObservableRead, KindMutation, KindPersistentAction:
		return true
	default:
		return false
	}
}

// NeedsMutation reports whether the turn requires an observable state change.
// An explicit read-only constraint always wins: the user forbidding changes is
// a stronger signal than any verb they used to describe the subject.
func (t TurnIntent) NeedsMutation() bool {
	if t.ReadOnlyConstraint {
		return false
	}
	return t.Kind == KindMutation || t.Kind == KindPersistentAction
}

// NeedsPersistentAction reports whether the turn requires a durable write that
// outlives the session.
func (t TurnIntent) NeedsPersistentAction() bool {
	if t.ReadOnlyConstraint {
		return false
	}
	return t.Kind == KindPersistentAction
}

// NeedsGoalWriteBudget reports whether a Goal objective should start on the
// extended write turn budget.
//
// It deliberately differs from NeedsMutation: a Goal is a long-horizon objective,
// so a bare fault report is work to be fixed unless the user asked only to
// understand it. Ordinary Delivery keeps the stricter reading, which is why this
// derivation lives here rather than being folded into Kind.
func (t TurnIntent) NeedsGoalWriteBudget() bool {
	if t.NeedsMutation() {
		return true
	}
	if !t.FaultReport || t.ReadOnlyConstraint {
		return false
	}
	// A fault the user only wants explained or diagnosed stays consultative.
	// DiagnosticIntent covers wording that scopes the work to understanding
	// without forbidding a change outright.
	if t.DiagnosticIntent {
		return false
	}
	return t.Kind != KindAdvisory && t.Kind != KindConversation
}
