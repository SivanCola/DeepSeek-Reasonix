package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"reasonix/internal/agent"
)

// ErrStopped is returned when the user chooses "stop task" on a recovery card.
var ErrStopped = errors.New("recovery checkpoint: task stopped by user")

// ModeProvider reports the current tool-approval mode (ask|auto|yolo).
type ModeProvider func() string

// EmitPromptFunc shows a fresh recovery confirmation card and returns its id.
// It must not grant session or persistent authorization. The gate waits until
// Resolve is called for that id (or ctx ends).
type EmitPromptFunc func(ctx context.Context, taskID string, pending PendingProposal, failure *FailureEvent) (approvalID string, err error)

// Reviewer evaluates ambiguous recovery proposals.
type Reviewer interface {
	Review(ctx context.Context, failure *FailureEvent, diagnosis []string, proposal Proposal, taskSummary string) (ReviewVerdict, error)
}

// TailInjector injects a one-shot dynamic user-tail after a failure arms the
// checkpoint. Hard constraints still come from this gate.
type TailInjector func(taskID, message string)

// CancelFunc cancels the root agent and all current-task sub-agents.
type CancelFunc func()

// Options configures a Gate.
type Options struct {
	Enabled        bool
	Mode           ModeProvider
	EmitPrompt     EmitPromptFunc
	Reviewer       Reviewer
	InjectTail     TailInjector
	Cancel         CancelFunc
	TaskSummary    func() string
	MaxDiagRepeats int // reserved for identical diagnostic-failure threshold
	Now            func() time.Time
	// Headless, when true, never waits for a human: blocks the mutation with a
	// structured blocker message instead.
	Headless bool
	// Persist is invoked after meaningful state changes (optional).
	Persist func(Snapshot)
}

// Gate is the shared recovery state machine for one controller session.
// Root, foreground sub-agents, and background writer sub-agents share it;
// state is isolated by TaskID.
type Gate struct {
	mu      sync.Mutex
	enabled atomic.Bool
	opts    Options
	tasks   map[string]*TaskState
	metrics Metrics
	waiters map[string]chan resolvePayload // keyed by approval id
	taskOf  map[string]string              // approval id -> task id
}

type resolvePayload struct {
	action   Action
	feedback string
}

// NewGate constructs a recovery gate. When opts.Enabled is false, all checks
// are no-ops until SetEnabled(true).
func NewGate(opts Options) *Gate {
	if opts.Mode == nil {
		opts.Mode = func() string { return "auto" }
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.MaxDiagRepeats <= 0 {
		opts.MaxDiagRepeats = 3
	}
	g := &Gate{
		opts:    opts,
		tasks:   map[string]*TaskState{},
		waiters: map[string]chan resolvePayload{},
		taskOf:  map[string]string{},
	}
	g.enabled.Store(opts.Enabled)
	return g
}

// SetEnabled toggles the checkpoint for this session.
func (g *Gate) SetEnabled(enabled bool) {
	if g == nil {
		return
	}
	g.enabled.Store(enabled)
}

// Enabled reports whether the checkpoint is enabled for this session.
func (g *Gate) Enabled() bool {
	if g == nil {
		return false
	}
	return g.enabled.Load()
}

// Metrics returns a copy of content-free counters.
func (g *Gate) Metrics() Metrics {
	if g == nil {
		return Metrics{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.metrics
}

// Snapshot returns a copy of task state for persistence.
func (g *Gate) Snapshot() Snapshot {
	if g == nil {
		return Snapshot{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	out := Snapshot{Tasks: map[string]*TaskState{}}
	for id, st := range g.tasks {
		if st == nil {
			continue
		}
		cp := *st
		if st.Failure != nil {
			f := *st.Failure
			cp.Failure = &f
		}
		if st.Pending != nil {
			p := *st.Pending
			cp.Pending = &p
		}
		out.Tasks[id] = &cp
	}
	return out
}

// Restore loads persisted task state (e.g. after restart / controller rebuild).
// Unresolved awaiting prompts lose their live reply channel, so phase is demoted
// to diagnosing while Failure (and last Pending for UI replay) is preserved.
// The next mutation must re-prompt; cold-start never silently continues past a
// checkpoint.
func (g *Gate) Restore(snap Snapshot) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tasks = map[string]*TaskState{}
	for id, st := range snap.Tasks {
		if st == nil {
			continue
		}
		cp := *st
		if st.Failure != nil {
			f := *st.Failure
			cp.Failure = &f
		}
		if st.Pending != nil {
			p := *st.Pending
			cp.Pending = &p
		}
		if cp.Phase == PhaseAwaitingDecision || cp.Phase == PhaseApprovedOnce {
			// Drop one-shot grants and live waiters across process death.
			cp.Phase = PhaseDiagnosing
			cp.ApprovedFinger = ""
			cp.ApprovalID = ""
			// Keep Pending for ReplayUnresolved so the UI can re-show the card.
		}
		if cp.Failure == nil {
			cp.Phase = PhaseIdle
			cp.Pending = nil
		} else if cp.Phase == PhaseIdle {
			cp.Phase = PhaseDiagnosing
		}
		g.tasks[id] = &cp
	}
}

// UnresolvedForReplay returns failure events that still need a human decision
// after restore (or mid-session). Used to re-emit recovery cards on cold start
// / ReplayPendingPrompts without holding write locks.
func (g *Gate) UnresolvedForReplay() []struct {
	TaskID  string
	Failure FailureEvent
	Pending *PendingProposal
} {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []struct {
		TaskID  string
		Failure FailureEvent
		Pending *PendingProposal
	}
	for id, st := range g.tasks {
		if st == nil || st.Failure == nil {
			continue
		}
		if st.Phase != PhaseDiagnosing && st.Phase != PhaseAwaitingDecision {
			continue
		}
		item := struct {
			TaskID  string
			Failure FailureEvent
			Pending *PendingProposal
		}{TaskID: id, Failure: *st.Failure}
		if st.Pending != nil {
			p := *st.Pending
			item.Pending = &p
		}
		out = append(out, item)
	}
	return out
}

// BindApprovalID associates a prompt id with the task waiting on it so
// Resolve can find the waiter after EmitPrompt returns. If a provisional
// waiter is parked under pending:<taskID>, it is re-keyed to approvalID.
func (g *Gate) BindApprovalID(taskID, approvalID string) {
	if g == nil {
		return
	}
	taskID = normalizeTaskID(taskID)
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.taskLocked(taskID)
	st.ApprovalID = approvalID
	provisional := "pending:" + taskID
	if ch := g.waiters[provisional]; ch != nil {
		delete(g.waiters, provisional)
		delete(g.taskOf, provisional)
		g.waiters[approvalID] = ch
	}
	g.taskOf[approvalID] = taskID
}

// Resolve applies a user decision to a pending recovery approval.
// action is continue|revise|stop. For revise, feedback is injected as steer
// and the current mutation is refused in the same operation.
func (g *Gate) Resolve(id string, action Action, feedback string) error {
	if g == nil {
		return fmt.Errorf("recovery gate is nil")
	}
	id = strings.TrimSpace(id)
	g.mu.Lock()
	ch := g.waiters[id]
	taskID := g.taskOf[id]
	if ch == nil || taskID == "" {
		g.mu.Unlock()
		return fmt.Errorf("unknown recovery approval %q", id)
	}
	st := g.taskLocked(taskID)
	switch action {
	case ActionContinue:
		if st.Pending != nil {
			st.ApprovedFinger = st.Pending.Fingerprint
			st.Phase = PhaseApprovedOnce
		} else {
			st.Phase = PhaseDiagnosing
		}
		g.metrics.HumanContinues++
	case ActionRevise:
		st.Phase = PhaseDiagnosing
		st.ApprovedFinger = ""
		st.Pending = nil
		g.metrics.HumanRevises++
	case ActionStop:
		st.Phase = PhaseIdle
		st.Failure = nil
		st.Pending = nil
		st.ApprovedFinger = ""
		g.metrics.HumanStops++
	default:
		g.mu.Unlock()
		return fmt.Errorf("unknown recovery action %q", action)
	}
	st.ApprovalID = ""
	delete(g.waiters, id)
	delete(g.taskOf, id)
	g.mu.Unlock()

	select {
	case ch <- resolvePayload{action: action, feedback: feedback}:
	default:
	}
	if action == ActionStop && g.opts.Cancel != nil {
		g.opts.Cancel()
	}
	g.persist()
	return nil
}

// ObserveResult implements agent.RecoveryGate.
func (g *Gate) ObserveResult(_ context.Context, obs Observation) {
	if g == nil || !g.enabled.Load() || !g.activeMode() {
		return
	}
	taskID := normalizeTaskID(obs.TaskID)

	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.taskLocked(taskID)

	// Successful host-recognized verification clears the failure event.
	if obs.Success && obs.Verification {
		g.clearFailureLocked(st)
		g.persistUnlocked()
		return
	}
	// Any successful mutation ends the current failure event.
	if obs.Success && obs.Mutates {
		g.clearFailureLocked(st)
		g.persistUnlocked()
		return
	}
	// Diagnostic read successes do not clear failure state.
	if obs.Success {
		return
	}
	if !QualifyingFailure(obs) {
		return
	}

	// Already recovering and another recovery op failed: raise repeat count.
	if st.Phase != PhaseIdle && st.Failure != nil {
		st.ConsecutiveFails++
		st.Failure.RepeatCount++
		st.Failure.ErrSummary = firstNonEmpty(obs.ErrSummary, st.Failure.ErrSummary)
		st.Failure.OutputExcerpt = clip(obs.Output, 1500)
		st.Phase = PhaseDiagnosing
		st.ApprovedFinger = ""
		st.Pending = nil
		g.metrics.FailureEvents++
		g.maybeInjectTailLocked(taskID, st)
		return
	}

	fp := CallFingerprint(obs.Tool, obs.Subject, "", obs.Args)
	st.Failure = &FailureEvent{
		Tool:          obs.Tool,
		ArgsSummary:   ArgsSummary(obs.Args, 200),
		Subject:       obs.Subject,
		ErrSummary:    obs.ErrSummary,
		OutputExcerpt: clip(obs.Output, 1500),
		SourceAgent:   obs.AgentID,
		TaskID:        taskID,
		ReadOnly:      obs.ReadOnly,
		Verification:  obs.Verification,
		Mutates:       obs.Mutates,
		RepeatCount:   1,
		CreatedAt:     g.opts.Now(),
		Args:          append(json.RawMessage(nil), obs.Args...),
		Fingerprint:   fp,
		SafeRetryLeft: 1,
	}
	st.Phase = PhaseDiagnosing
	st.ConsecutiveFails = 1
	st.ApprovedFinger = ""
	st.Pending = nil
	st.TailInjected = false
	g.metrics.FailureEvents++
	g.maybeInjectTailLocked(taskID, st)
}

// BeforeMutation implements agent.RecoveryGate.
func (g *Gate) BeforeMutation(ctx context.Context, proposal Proposal) (Decision, error) {
	if g == nil || !g.enabled.Load() || !g.activeMode() {
		return Decision{Allow: true}, nil
	}
	// Host-proven read-only diagnostics always continue.
	if proposal.ReadOnly && !proposal.Mutates {
		return Decision{Allow: true}, nil
	}
	if !proposal.Mutates && !proposal.Verification {
		return Decision{Allow: true}, nil
	}

	taskID := normalizeTaskID(proposal.TaskID)
	fp := CallFingerprint(proposal.Tool, proposal.Subject, proposal.Preview, proposal.Args)

	g.mu.Lock()
	st := g.taskLocked(taskID)
	if st.Phase == PhaseIdle || st.Failure == nil {
		g.mu.Unlock()
		return Decision{Allow: true}, nil
	}

	// One-shot approved fingerprint.
	if st.Phase == PhaseApprovedOnce && st.ApprovedFinger != "" {
		if fp == st.ApprovedFinger {
			st.ApprovedFinger = ""
			st.Phase = PhaseDiagnosing
			g.mu.Unlock()
			return Decision{Allow: true, ConsumedApprovedOnce: true}, nil
		}
		st.Phase = PhaseDiagnosing
		st.ApprovedFinger = ""
	}

	failure := *st.Failure
	consec := st.ConsecutiveFails
	diagNotes := append([]string(nil), st.Failure.DiagnosisNotes...)
	g.mu.Unlock()

	// Enrich proposal flags from deterministic classifiers when callers omit them.
	if !proposal.HighRisk {
		proposal.HighRisk = IsHighRiskMutation(proposal)
	}
	if !proposal.ExpandedScope {
		proposal.ExpandedScope = ScopeExpanded(&failure, proposal)
	}
	if !proposal.StrategyChanged {
		proposal.StrategyChanged = StrategyChanged(&failure, proposal)
	}
	if proposal.SafeRetry || IsSafeVerificationRetry(&failure, proposal) {
		g.mu.Lock()
		if st := g.taskLocked(taskID); st.Failure != nil && st.Failure.SafeRetryLeft > 0 {
			st.Failure.SafeRetryLeft--
			g.metrics.RuleContinues++
		}
		g.mu.Unlock()
		g.persist()
		return Decision{Allow: true}, nil
	}

	forceConfirm := proposal.HighRisk ||
		proposal.ExpandedScope ||
		proposal.StrategyChanged ||
		consec >= 2

	var verdict ReviewVerdict
	if !forceConfirm {
		if g.opts.Reviewer != nil {
			start := g.opts.Now()
			taskSummary := ""
			if g.opts.TaskSummary != nil {
				taskSummary = g.opts.TaskSummary()
			}
			v, err := g.opts.Reviewer.Review(ctx, &failure, diagNotes, proposal, taskSummary)
			latency := g.opts.Now().Sub(start).Milliseconds()
			g.mu.Lock()
			g.metrics.ReviewLatencyMsSum += latency
			g.metrics.ReviewLatencyCount++
			if err != nil {
				g.metrics.ReviewErrors++
			}
			g.mu.Unlock()
			if err != nil {
				forceConfirm = true
				verdict = ReviewVerdict{
					Outcome:        ReviewConfirm,
					ChangeKind:     ChangeUncertain,
					FailureSummary: failure.ErrSummary,
					Diagnosis:      "recovery reviewer unavailable",
					ProposedAction: firstNonEmpty(proposal.Subject, proposal.Tool),
					Rationale:      "reviewer error: " + err.Error(),
				}
			} else {
				verdict = normalizeVerdict(v, &failure, proposal, diagNotes)
				if verdict.Outcome == ReviewContinue && verdict.ChangeKind == ChangeSameStrategy {
					g.mu.Lock()
					g.metrics.ReviewContinues++
					g.mu.Unlock()
					return Decision{Allow: true}, nil
				}
				// Any non-continue or non-same-strategy outcome needs a human.
				forceConfirm = true
			}
		} else {
			forceConfirm = true
			verdict = ReviewVerdict{
				Outcome:        ReviewConfirm,
				ChangeKind:     ChangeUncertain,
				FailureSummary: failure.ErrSummary,
				Diagnosis:      strings.Join(diagNotes, "\n"),
				ProposedAction: firstNonEmpty(proposal.Subject, proposal.Preview, proposal.Tool),
				Rationale:      "no recovery reviewer configured; confirming before mutation",
			}
		}
	} else {
		kind := ChangeUncertain
		switch {
		case proposal.HighRisk || consec >= 2:
			kind = ChangeRisk
		case proposal.ExpandedScope:
			kind = ChangeScope
		case proposal.StrategyChanged:
			kind = ChangeStrategy
		}
		verdict = ReviewVerdict{
			Outcome:        ReviewConfirm,
			ChangeKind:     kind,
			FailureSummary: failure.ErrSummary,
			Diagnosis:      strings.Join(diagNotes, "\n"),
			ProposedAction: firstNonEmpty(proposal.Subject, proposal.Preview, proposal.Tool),
			Rationale:      ruleRationale(kind, consec),
		}
	}

	pending := PendingProposal{
		Tool:        proposal.Tool,
		Subject:     proposal.Subject,
		Preview:     proposal.Preview,
		Args:        append(json.RawMessage(nil), proposal.Args...),
		Fingerprint: fp,
		SourceAgent: firstNonEmpty(proposal.AgentID, failure.SourceAgent),
		ChangeKind:  verdict.ChangeKind,
		Rationale:   firstNonEmpty(verdict.Rationale, verdict.Diagnosis),
		Diagnosis:   firstNonEmpty(verdict.Diagnosis, strings.Join(diagNotes, "\n")),
		Failure:     firstNonEmpty(verdict.FailureSummary, failure.ErrSummary),
		Proposed:    firstNonEmpty(verdict.ProposedAction, proposal.Subject, proposal.Preview),
	}

	if g.opts.Headless || g.opts.EmitPrompt == nil {
		return Decision{
			Allow:   false,
			Blocked: true,
			Message: headlessBlockerMessage(pending, &failure),
		}, nil
	}

	// Create the waiter channel before EmitPrompt. Resolve may race in as soon
	// as the approval id is known (desktop/bot), so re-key the waiter under the
	// real id immediately after EmitPrompt returns.
	reply := make(chan resolvePayload, 1)
	g.mu.Lock()
	st = g.taskLocked(taskID)
	st.Phase = PhaseAwaitingDecision
	st.Pending = &pending
	g.metrics.HumanPrompts++
	if st.ConsecutiveFails > 1 {
		g.metrics.RepeatPrompts++
	}
	// Park under a provisional task key until the real approval id is known.
	provisional := "pending:" + taskID
	g.waiters[provisional] = reply
	g.taskOf[provisional] = taskID
	g.mu.Unlock()

	approvalID, err := g.opts.EmitPrompt(ctx, taskID, pending, &failure)
	if err != nil {
		g.mu.Lock()
		delete(g.waiters, provisional)
		delete(g.taskOf, provisional)
		if st := g.tasks[taskID]; st != nil {
			st.Phase = PhaseDiagnosing
			st.Pending = nil
			st.ApprovalID = ""
		}
		g.mu.Unlock()
		return Decision{Allow: false, Blocked: true, Message: "blocked: recovery prompt failed: " + err.Error()}, err
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		g.mu.Lock()
		delete(g.waiters, provisional)
		delete(g.taskOf, provisional)
		if st := g.tasks[taskID]; st != nil {
			st.Phase = PhaseDiagnosing
			st.Pending = nil
		}
		g.mu.Unlock()
		return Decision{Allow: false, Blocked: true, Message: "blocked: recovery prompt returned empty id"}, fmt.Errorf("empty recovery approval id")
	}

	g.mu.Lock()
	delete(g.waiters, provisional)
	delete(g.taskOf, provisional)
	// If Resolve already re-keyed via BindApprovalID, keep the live waiter.
	if existing, ok := g.waiters[approvalID]; ok && existing != nil {
		reply = existing
	} else {
		g.waiters[approvalID] = reply
	}
	st = g.taskLocked(taskID)
	st.ApprovalID = approvalID
	g.taskOf[approvalID] = taskID
	g.mu.Unlock()
	g.persist()

	select {
	case payload := <-reply:
		return g.decisionFromResolve(taskID, fp, payload)
	case <-ctx.Done():
		g.mu.Lock()
		delete(g.waiters, approvalID)
		delete(g.taskOf, approvalID)
		if st := g.tasks[taskID]; st != nil {
			st.Phase = PhaseDiagnosing
			st.Pending = nil
			st.ApprovalID = ""
		}
		g.mu.Unlock()
		return Decision{Allow: false, Blocked: true, Message: "blocked: recovery confirmation cancelled"}, ctx.Err()
	}
}

func (g *Gate) decisionFromResolve(taskID, fp string, payload resolvePayload) (Decision, error) {
	switch payload.action {
	case ActionContinue:
		g.mu.Lock()
		st := g.taskLocked(taskID)
		// Consume the one-shot grant immediately for this call.
		if st.Phase == PhaseApprovedOnce && st.ApprovedFinger == fp {
			st.ApprovedFinger = ""
			st.Phase = PhaseDiagnosing
			g.mu.Unlock()
			return Decision{Allow: true, ConsumedApprovedOnce: true}, nil
		}
		st.Phase = PhaseDiagnosing
		st.Pending = nil
		g.mu.Unlock()
		return Decision{Allow: true}, nil
	case ActionRevise:
		msg := "blocked: user requested a revised recovery plan"
		if strings.TrimSpace(payload.feedback) != "" {
			msg += ": " + strings.TrimSpace(payload.feedback)
			if g.opts.InjectTail != nil {
				g.opts.InjectTail(taskID, "Recovery revision from the user (apply before the next mutation):\n"+strings.TrimSpace(payload.feedback))
			}
		}
		return Decision{Allow: false, Blocked: true, Message: msg}, nil
	case ActionStop:
		return Decision{Allow: false, Blocked: true, Message: "blocked: user stopped the task at the recovery checkpoint"}, ErrStopped
	default:
		return Decision{Allow: false, Blocked: true, Message: "blocked: unknown recovery action"}, nil
	}
}

// RecordDiagnosis appends a diagnosis note while in diagnosing phase.
func (g *Gate) RecordDiagnosis(taskID, note string) {
	if g == nil || strings.TrimSpace(note) == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.taskLocked(normalizeTaskID(taskID))
	if st.Failure == nil {
		return
	}
	st.Failure.DiagnosisNotes = append(st.Failure.DiagnosisNotes, strings.TrimSpace(note))
	if len(st.Failure.DiagnosisNotes) > 12 {
		st.Failure.DiagnosisNotes = st.Failure.DiagnosisNotes[len(st.Failure.DiagnosisNotes)-12:]
	}
}

// --- internals ---

func (g *Gate) activeMode() bool {
	mode := strings.ToLower(strings.TrimSpace(g.opts.Mode()))
	return mode == "auto"
}

func (g *Gate) taskLocked(taskID string) *TaskState {
	st := g.tasks[taskID]
	if st == nil {
		st = &TaskState{Phase: PhaseIdle}
		g.tasks[taskID] = st
	}
	return st
}

func (g *Gate) clearFailureLocked(st *TaskState) {
	st.Phase = PhaseIdle
	st.Failure = nil
	st.Pending = nil
	st.ApprovedFinger = ""
	st.ApprovalID = ""
	st.ConsecutiveFails = 0
	st.DiagnosingReads = 0
	st.TailInjected = false
}

func (g *Gate) maybeInjectTailLocked(taskID string, st *TaskState) {
	if st.TailInjected || g.opts.InjectTail == nil {
		return
	}
	st.TailInjected = true
	msg := "A recovery checkpoint is active after a tool/verification failure. " +
		"Diagnose with read-only tools first (read logs, search code, inspect the failing command output). " +
		"Before changing strategy, scope, or risk of the next write, explain the diagnosis and the proposed recovery step. " +
		"The host will pause unconfirmed strategy/scope/risk changes automatically."
	go g.opts.InjectTail(taskID, msg)
}

func (g *Gate) persist() {
	if g == nil || g.opts.Persist == nil {
		return
	}
	g.opts.Persist(g.Snapshot())
}

func (g *Gate) persistUnlocked() {
	// Caller holds g.mu. Snapshot needs the lock — build inline.
	if g == nil || g.opts.Persist == nil {
		return
	}
	out := Snapshot{Tasks: map[string]*TaskState{}}
	for id, st := range g.tasks {
		if st == nil {
			continue
		}
		cp := *st
		if st.Failure != nil {
			f := *st.Failure
			cp.Failure = &f
		}
		if st.Pending != nil {
			p := *st.Pending
			cp.Pending = &p
		}
		out.Tasks[id] = &cp
	}
	// Persist outside lock to avoid deadlocks if Persist re-enters.
	go g.opts.Persist(out)
}

func normalizeTaskID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "root"
	}
	return id
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func ruleRationale(kind ChangeKind, consec int) string {
	switch kind {
	case ChangeRisk:
		if consec >= 2 {
			return "a second recovery failure requires confirmation before further writes"
		}
		return "the proposed mutation is high risk (delete, install, config, or external write)"
	case ChangeScope:
		return "the proposed write expands beyond the original failure scope"
	case ChangeStrategy:
		return "the proposed recovery uses a different method than the failed approach"
	default:
		return "the host cannot prove this recovery is the same strategy and scope"
	}
}

func headlessBlockerMessage(pending PendingProposal, failure *FailureEvent) string {
	var b strings.Builder
	b.WriteString("blocked: recovery checkpoint requires human confirmation, but this environment has no decision channel.\n")
	if failure != nil {
		b.WriteString("Failure: ")
		b.WriteString(firstNonEmpty(failure.ErrSummary, failure.Tool))
		b.WriteString("\n")
	}
	if pending.Diagnosis != "" {
		b.WriteString("Diagnosis: ")
		b.WriteString(pending.Diagnosis)
		b.WriteString("\n")
	}
	b.WriteString("Proposed: ")
	b.WriteString(firstNonEmpty(pending.Proposed, pending.Subject, pending.Tool))
	b.WriteString("\n")
	if pending.Rationale != "" {
		b.WriteString("Why confirm: ")
		b.WriteString(pending.Rationale)
	}
	return b.String()
}

func normalizeVerdict(v ReviewVerdict, failure *FailureEvent, proposal Proposal, diagNotes []string) ReviewVerdict {
	switch strings.ToLower(strings.TrimSpace(string(v.Outcome))) {
	case "continue":
		v.Outcome = ReviewContinue
	case "confirm":
		v.Outcome = ReviewConfirm
	default:
		// Unparseable/unknown outcome fails closed.
		v.Outcome = ReviewConfirm
		if v.ChangeKind == "" {
			v.ChangeKind = ChangeUncertain
		}
	}
	switch ChangeKind(strings.ToLower(strings.TrimSpace(string(v.ChangeKind)))) {
	case ChangeSameStrategy, ChangeStrategy, ChangeScope, ChangeRisk, ChangeUncertain:
		v.ChangeKind = ChangeKind(strings.ToLower(strings.TrimSpace(string(v.ChangeKind))))
	default:
		if v.Outcome == ReviewContinue {
			// Cannot silently continue without a clear same_strategy label.
			v.Outcome = ReviewConfirm
		}
		v.ChangeKind = ChangeUncertain
	}
	if strings.TrimSpace(v.FailureSummary) == "" && failure != nil {
		v.FailureSummary = failure.ErrSummary
	}
	if strings.TrimSpace(v.Diagnosis) == "" {
		v.Diagnosis = strings.Join(diagNotes, "\n")
	}
	if strings.TrimSpace(v.ProposedAction) == "" {
		v.ProposedAction = firstNonEmpty(proposal.Subject, proposal.Preview, proposal.Tool)
	}
	if strings.TrimSpace(v.Rationale) == "" {
		v.Rationale = "recovery reviewer provided no rationale"
	}
	return v
}

// Ensure Gate implements agent.RecoveryGate.
var _ agent.RecoveryGate = (*Gate)(nil)
