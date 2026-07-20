package recovery

import (
	"encoding/json"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
)

// Phase is the per-task recovery state-machine phase.
type Phase string

const (
	PhaseIdle             Phase = "idle"
	PhaseDiagnosing       Phase = "diagnosing"
	PhaseAwaitingDecision Phase = "awaiting_decision"
	PhaseApprovedOnce     Phase = "approved_once"
)

// ChangeKind classifies how the proposed recovery action differs from the
// original approach.
type ChangeKind string

const (
	ChangeSameStrategy ChangeKind = "same_strategy"
	ChangeStrategy     ChangeKind = "strategy"
	ChangeScope        ChangeKind = "scope"
	ChangeRisk         ChangeKind = "risk"
	ChangeUncertain    ChangeKind = "uncertain"
)

// ReviewOutcome is the independent recovery reviewer's decision.
type ReviewOutcome string

const (
	ReviewContinue ReviewOutcome = "continue"
	ReviewConfirm  ReviewOutcome = "confirm"
)

// ReviewVerdict is the strict JSON shape the recovery reviewer must produce.
type ReviewVerdict struct {
	Outcome        ReviewOutcome `json:"outcome"`
	ChangeKind     ChangeKind    `json:"change_kind"`
	FailureSummary string        `json:"failure_summary"`
	Diagnosis      string        `json:"diagnosis"`
	ProposedAction string        `json:"proposed_action"`
	Rationale      string        `json:"rationale"`
}

// FailureEvent records the active failure that armed the checkpoint.
type FailureEvent struct {
	Tool           string          `json:"tool"`
	ArgsSummary    string          `json:"args_summary,omitempty"`
	Subject        string          `json:"subject,omitempty"`
	ErrSummary     string          `json:"err_summary,omitempty"`
	OutputExcerpt  string          `json:"output_excerpt,omitempty"`
	SourceAgent    string          `json:"source_agent,omitempty"`
	TaskID         string          `json:"task_id,omitempty"`
	ReadOnly       bool            `json:"read_only,omitempty"`
	Verification   bool            `json:"verification,omitempty"`
	Mutates        bool            `json:"mutates,omitempty"`
	RepeatCount    int             `json:"repeat_count,omitempty"`
	CreatedAt      time.Time       `json:"created_at,omitempty"`
	Args           json.RawMessage `json:"args,omitempty"`
	Fingerprint    string          `json:"fingerprint,omitempty"`
	SafeRetryLeft  int             `json:"safe_retry_left,omitempty"`
	DiagnosisNotes []string        `json:"diagnosis_notes,omitempty"`
}

// PendingProposal is the mutation paused for user confirmation.
type PendingProposal struct {
	Tool        string          `json:"tool"`
	Subject     string          `json:"subject,omitempty"`
	Preview     string          `json:"preview,omitempty"`
	Args        json.RawMessage `json:"args,omitempty"`
	Fingerprint string          `json:"fingerprint,omitempty"`
	SourceAgent string          `json:"source_agent,omitempty"`
	ChangeKind  ChangeKind      `json:"change_kind,omitempty"`
	Rationale   string          `json:"rationale,omitempty"`
	Diagnosis   string          `json:"diagnosis,omitempty"`
	Failure     string          `json:"failure,omitempty"`
	Proposed    string          `json:"proposed,omitempty"`
}

// TaskState is the recovery state for one task.
type TaskState struct {
	Phase            Phase            `json:"phase"`
	Failure          *FailureEvent    `json:"failure,omitempty"`
	Pending          *PendingProposal `json:"pending,omitempty"`
	ApprovedFinger   string           `json:"approved_fingerprint,omitempty"`
	ApprovalID       string           `json:"approval_id,omitempty"`
	PendingGuidance  string           `json:"pending_guidance,omitempty"`
	ConsecutiveFails int              `json:"consecutive_fails,omitempty"`
	DiagnosingReads  int              `json:"diagnosing_reads,omitempty"`
	TailInjected     bool             `json:"tail_injected,omitempty"`
}

// Snapshot is the persistable form of all task recovery state.
type Snapshot struct {
	Tasks map[string]*TaskState `json:"tasks,omitempty"`
}

// Metrics are content-free counters for release observation.
type Metrics struct {
	FailureEvents      int64
	RuleContinues      int64
	ReviewContinues    int64
	HumanPrompts       int64
	HumanContinues     int64
	HumanRevises       int64
	HumanStops         int64
	ReviewErrors       int64
	ReviewLatencyMsSum int64
	ReviewLatencyCount int64
	RepeatPrompts      int64
}

// ApprovalKindRecovery is the Approval.Kind value for recovery cards.
const ApprovalKindRecovery = "recovery"

// ApprovalKindTool and ApprovalKindPlan keep ordinary approval kinds explicit.
const (
	ApprovalKindTool = "tool"
	ApprovalKindPlan = "plan"
)

// ToEventApproval builds the event payload for a recovery confirmation card.
func ToEventApproval(id string, pending PendingProposal, failure *FailureEvent) event.Approval {
	rec := &event.RecoveryApproval{
		SourceAgent:     pending.SourceAgent,
		FailedTool:      "",
		FailedSummary:   pending.Failure,
		Diagnosis:       pending.Diagnosis,
		NextTool:        pending.Tool,
		NextAction:      firstNonEmpty(pending.Proposed, pending.Subject, pending.Preview),
		ChangeKind:      string(pending.ChangeKind),
		ChangeRationale: pending.Rationale,
		ReviewRationale: pending.Rationale,
	}
	if failure != nil {
		rec.FailedTool = failure.Tool
		if rec.FailedSummary == "" {
			rec.FailedSummary = failure.ErrSummary
		}
		if rec.SourceAgent == "" {
			rec.SourceAgent = failure.SourceAgent
		}
	}
	subject := firstNonEmpty(pending.Subject, pending.Preview, pending.Tool)
	reason := firstNonEmpty(pending.Rationale, pending.Diagnosis, "recovery checkpoint requires confirmation")
	return event.Approval{
		ID:       id,
		Tool:     pending.Tool,
		Subject:  subject,
		Reason:   reason,
		Fresh:    true,
		Kind:     ApprovalKindRecovery,
		Recovery: rec,
	}
}

// Observation aliases keep call sites readable when bridging agent types.
type Observation = agent.RecoveryObservation
type Proposal = agent.RecoveryProposal
type Decision = agent.RecoveryDecision
type Action = agent.RecoveryAction

const (
	ActionContinue = agent.RecoveryActionContinue
	ActionRevise   = agent.RecoveryActionRevise
	ActionStop     = agent.RecoveryActionStop
)

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
