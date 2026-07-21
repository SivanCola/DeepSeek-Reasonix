package recovery

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"reasonix/internal/agent"
)

// ModeProvider reports the current tool-approval mode (ask|auto|yolo).
type ModeProvider func() string

// EmitPromptFunc shows a fresh Auto Guard card and returns its id.
// It must not grant session or persistent authorization. The gate waits until
// Resolve is called for that id (or ctx ends).
type EmitPromptFunc func(ctx context.Context, taskID string, pending PendingProposal, failure *FailureEvent) (approvalID string, err error)

// Reviewer evaluates ambiguous failure-recovery proposals.
type Reviewer interface {
	Review(ctx context.Context, failure *FailureEvent, diagnosis []string, proposal Proposal, taskSummary string) (ReviewVerdict, error)
}

// Options configures a Gate.
type Options struct {
	Enabled         bool
	Mode            ModeProvider
	EmitPrompt      EmitPromptFunc
	Reviewer        Reviewer
	TaskSummary     func() string
	MaxReviewBlocks int // consecutive reviewer blocks before human escalation
	Now             func() time.Time
	// Headless, when true, never waits for a human: blocks the mutation with a
	// structured blocker message instead.
	Headless bool
	// PersistenceKey is sampled synchronously when a state change is scheduled.
	// Persist receives that captured key so an asynchronous write cannot follow
	// a later session switch and land in the wrong sidecar.
	PersistenceKey func() string
	// Persist is invoked after meaningful state changes (optional).
	Persist func(key string, snapshot Snapshot)
}

// Gate is the shared Auto Guard state machine for one controller session.
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

	// persistMu orders asynchronous snapshots. A newer state may be scheduled
	// before an older goroutine reaches disk; sequence checks prevent that older
	// snapshot from overwriting the newer checkpoint.
	persistMu   sync.Mutex
	persistSeq  uint64
	persistCond *sync.Cond
	// persistPending and persistDone are tracked per session key so old and new
	// sessions can drain independently without retaining keys after completion.
	persistPending map[string]int
	persistDone    map[string]uint64
}

type resolvePayload struct {
	action   Action
	feedback string
}

// NewGate constructs Auto Guard. When opts.Enabled is false, all checks
// are no-ops until SetEnabled(true).
func NewGate(opts Options) *Gate {
	if opts.Mode == nil {
		opts.Mode = func() string { return "auto" }
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.MaxReviewBlocks <= 0 {
		opts.MaxReviewBlocks = 3
	}
	g := &Gate{
		opts:           opts,
		tasks:          map[string]*TaskState{},
		waiters:        map[string]chan resolvePayload{},
		taskOf:         map[string]string{},
		persistPending: map[string]int{},
		persistDone:    map[string]uint64{},
	}
	g.persistCond = sync.NewCond(&g.persistMu)
	g.enabled.Store(opts.Enabled)
	return g
}

// SetEnabled toggles Auto Guard for this session.
func (g *Gate) SetEnabled(enabled bool) {
	if g == nil {
		return
	}
	g.enabled.Store(enabled)
}

// Enabled reports whether Auto Guard is enabled for this session.
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

// FlushPersistence waits until every snapshot already scheduled for key has
// finished. Session destruction uses this before removing sidecars so a late
// asynchronous write cannot resurrect an artifact that was just deleted.
func (g *Gate) FlushPersistence(key string) {
	if g == nil || g.opts.Persist == nil {
		return
	}
	g.persistMu.Lock()
	for g.persistPending[key] > 0 {
		g.persistCond.Wait()
	}
	g.persistMu.Unlock()
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
		if cp := cloneTaskState(st); cp != nil && !taskStateEmpty(cp) {
			out.Tasks[id] = cp
		}
	}
	return out
}

// Restore loads persisted failure context after restart/controller rebuild.
// Live prompts and one-call grants are deliberately not replayed: the interrupted
// call no longer exists, so the next proposed action must be classified again.
func (g *Gate) Restore(snap Snapshot) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tasks = map[string]*TaskState{}
	g.waiters = map[string]chan resolvePayload{}
	g.taskOf = map[string]string{}
	for id, st := range snap.Tasks {
		if st == nil {
			continue
		}
		cp := cloneTaskState(st)
		if cp == nil {
			continue
		}
		if cp.Failure == nil {
			continue
		}
		cp.Phase = PhaseDiagnosing
		cp.Pending = nil
		cp.ApprovalID = ""
		g.tasks[id] = cp
	}
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

// Resolve applies a user decision to a pending Auto Guard approval.
// action is continue|revise. For revise, feedback is returned through the
// blocked tool result and the current mutation is refused in the same operation.
func (g *Gate) Resolve(id string, action Action, feedback string) error {
	if g == nil {
		return fmt.Errorf("recovery gate is nil")
	}
	id = strings.TrimSpace(id)
	g.mu.Lock()
	ch := g.waiters[id]
	taskID := g.taskOf[id]
	if taskID == "" {
		g.mu.Unlock()
		return fmt.Errorf("unknown recovery approval %q", id)
	}
	st := g.taskLocked(taskID)
	switch action {
	case ActionContinue:
		st.Phase = PhaseDiagnosing
		st.Pending = nil
		st.ReviewBlocks = 0
		g.metrics.HumanContinues++
	case ActionRevise:
		st.Phase = PhaseDiagnosing
		st.Pending = nil
		st.ReviewBlocks = 0
		g.metrics.HumanRevises++
	default:
		g.mu.Unlock()
		return fmt.Errorf("unknown recovery action %q", action)
	}
	st.ApprovalID = ""
	delete(g.waiters, id)
	delete(g.taskOf, id)
	if st.Failure == nil {
		delete(g.tasks, taskID)
	}
	g.mu.Unlock()

	if ch != nil {
		select {
		case ch <- resolvePayload{action: action, feedback: feedback}:
		default:
		}
	}
	g.persist()
	return nil
}

// ObserveResult implements agent.RecoveryGate. It returns one-shot guidance
// for the caller to enqueue on the exact Agent.Run that observed the failure.
func (g *Gate) ObserveResult(_ context.Context, obs Observation) string {
	if g == nil || !g.enabled.Load() || !g.activeMode() {
		return ""
	}
	taskID := normalizeTaskID(obs.TaskID)

	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.tasks[taskID]

	// Successful host-recognized verification clears the failure event.
	if obs.Success && obs.Verification {
		if st != nil {
			delete(g.tasks, taskID)
			g.persistUnlocked()
		}
		return ""
	}
	// Any successful mutation ends the current failure event.
	if obs.Success && obs.Mutates {
		if st != nil {
			delete(g.tasks, taskID)
			g.persistUnlocked()
		}
		return ""
	}
	// Diagnostic read successes do not clear failure state. Preserve a bounded
	// evidence excerpt for the isolated reviewer; otherwise it sees the failure
	// and proposed diff but none of the investigation that connected them.
	if obs.Success {
		if st != nil && st.Failure != nil && IsDiagnosticSuccess(obs) {
			if appendDiagnosisNoteLocked(st, diagnosticObservationNote(obs)) {
				g.persistUnlocked()
			}
		}
		return ""
	}
	if !QualifyingFailure(obs) {
		return ""
	}
	if st == nil {
		st = g.taskLocked(taskID)
	}

	// Already recovering and another recovery op failed: raise repeat count.
	if st.Phase != PhaseIdle && st.Failure != nil {
		st.ConsecutiveFails++
		st.Failure.RepeatCount++
		st.Failure.ErrSummary = firstNonEmpty(obs.ErrSummary, st.Failure.ErrSummary)
		st.Failure.OutputExcerpt = clip(obs.Output, 1500)
		st.Phase = PhaseDiagnosing
		st.Pending = nil
		g.metrics.FailureEvents++
		guidance := g.recoveryGuidanceLocked(st)
		g.persistUnlocked()
		return guidance
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
	st.ReviewBlocks = 0
	st.Pending = nil
	st.TailInjected = false
	g.metrics.FailureEvents++
	guidance := g.recoveryGuidanceLocked(st)
	g.persistUnlocked()
	return guidance
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
	// Deterministic boundary checks run before the failure-recovery path. This
	// gives Auto a real pre-action safety boundary without adding a model call to
	// ordinary workspace edits.
	if !proposal.HighRisk {
		proposal.HighRisk = IsHighRiskMutation(proposal)
	}

	taskID := normalizeTaskID(proposal.TaskID)
	fp := CallFingerprint(proposal.Tool, proposal.Subject, proposal.Preview, proposal.Args)

	g.mu.Lock()
	st := g.tasks[taskID]
	var failure *FailureEvent
	consec := 0
	var diagNotes []string
	if st != nil && st.Failure != nil {
		cp := *st.Failure
		cp.Args = append(json.RawMessage(nil), st.Failure.Args...)
		cp.DiagnosisNotes = append([]string(nil), st.Failure.DiagnosisNotes...)
		failure = &cp
		consec = st.ConsecutiveFails
		diagNotes = append([]string(nil), st.Failure.DiagnosisNotes...)
	}
	g.mu.Unlock()
	if failure == nil && !proposal.HighRisk {
		return Decision{Allow: true}, nil
	}

	if failure != nil && !proposal.ExpandedScope {
		proposal.ExpandedScope = ScopeExpanded(failure, proposal)
	}
	if failure != nil && !proposal.StrategyChanged {
		proposal.StrategyChanged = StrategyChanged(failure, proposal)
	}
	if failure != nil && (proposal.SafeRetry || IsSafeVerificationRetry(failure, proposal)) {
		g.mu.Lock()
		if st := g.taskLocked(taskID); st.Failure != nil && st.Failure.SafeRetryLeft > 0 {
			st.Failure.SafeRetryLeft--
			g.metrics.RuleContinues++
		}
		g.mu.Unlock()
		g.persist()
		return Decision{Allow: true}, nil
	}

	forceConfirm := failure == nil || proposal.HighRisk ||
		proposal.ExpandedScope ||
		proposal.StrategyChanged ||
		consec >= 2

	var verdict ReviewVerdict
	if !forceConfirm && failure != nil {
		if g.opts.Reviewer != nil {
			start := g.opts.Now()
			taskSummary := strings.TrimSpace(proposal.TaskSummary)
			if taskSummary == "" && g.opts.TaskSummary != nil {
				taskSummary = g.opts.TaskSummary()
			}
			v, err := g.opts.Reviewer.Review(ctx, failure, diagNotes, proposal, taskSummary)
			latency := g.opts.Now().Sub(start).Milliseconds()
			g.mu.Lock()
			g.metrics.ReviewLatencyMsSum += latency
			g.metrics.ReviewLatencyCount++
			if err != nil {
				g.metrics.ReviewErrors++
			}
			g.mu.Unlock()
			if err != nil {
				verdict = ReviewVerdict{
					Outcome:        ReviewConfirm,
					ChangeKind:     ChangeUncertain,
					FailureSummary: failure.ErrSummary,
					Diagnosis:      "Auto Guard reviewer unavailable",
					ProposedAction: firstNonEmpty(proposal.Subject, proposal.Tool),
					Rationale:      "reviewer error: " + err.Error(),
				}
			} else {
				verdict = normalizeVerdict(v, failure, proposal, diagNotes)
				if verdict.Outcome == ReviewContinue && verdict.ChangeKind == ChangeSameStrategy {
					g.mu.Lock()
					g.metrics.ReviewContinues++
					if current := g.tasks[taskID]; current != nil {
						current.ReviewBlocks = 0
					}
					g.mu.Unlock()
					g.persist()
					return Decision{Allow: true}, nil
				}
				// Give the exact reviewer reason back to the proposing agent first.
				// Only repeated inability to find a safe alternative escalates.
				blocks := g.recordReviewBlock(taskID, verdict)
				if blocks < g.opts.MaxReviewBlocks {
					return Decision{
						Allow:   false,
						Blocked: true,
						Message: reviewerBlockerMessage(verdict, blocks, g.opts.MaxReviewBlocks),
					}, nil
				}
				verdict.Rationale = fmt.Sprintf(
					"Auto Guard could not verify a safe alternative after %d consecutive attempts: %s",
					blocks, verdict.Rationale,
				)
			}
		} else {
			verdict = ReviewVerdict{
				Outcome:        ReviewConfirm,
				ChangeKind:     ChangeUncertain,
				FailureSummary: failure.ErrSummary,
				Diagnosis:      strings.Join(diagNotes, "\n"),
				ProposedAction: firstNonEmpty(proposal.Subject, proposal.Preview, proposal.Tool),
				Rationale:      "no Auto Guard reviewer configured; confirming before mutation",
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
		failureSummary := ""
		if failure != nil {
			failureSummary = failure.ErrSummary
		}
		verdict = ReviewVerdict{
			Outcome:        ReviewConfirm,
			ChangeKind:     kind,
			FailureSummary: failureSummary,
			Diagnosis:      strings.Join(diagNotes, "\n"),
			ProposedAction: firstNonEmpty(proposal.Subject, proposal.Preview, proposal.Tool),
			Rationale:      ruleRationale(kind, consec),
		}
	}

	failureSource := ""
	failureSummary := ""
	if failure != nil {
		failureSource = failure.SourceAgent
		failureSummary = failure.ErrSummary
	}
	pending := PendingProposal{
		Tool:        proposal.Tool,
		Subject:     proposal.Subject,
		Preview:     proposal.Preview,
		Args:        append(json.RawMessage(nil), proposal.Args...),
		Fingerprint: fp,
		SourceAgent: firstNonEmpty(proposal.AgentID, failureSource),
		ChangeKind:  verdict.ChangeKind,
		Rationale:   firstNonEmpty(verdict.Rationale, verdict.Diagnosis),
		Diagnosis:   firstNonEmpty(verdict.Diagnosis, strings.Join(diagNotes, "\n")),
		Failure:     firstNonEmpty(verdict.FailureSummary, failureSummary),
		Proposed:    firstNonEmpty(verdict.ProposedAction, proposal.Subject, proposal.Preview),
	}

	if g.opts.Headless || g.opts.EmitPrompt == nil {
		return Decision{
			Allow:   false,
			Blocked: true,
			Message: headlessBlockerMessage(pending, failure),
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

	approvalID, err := g.opts.EmitPrompt(ctx, taskID, pending, failure)
	if err != nil {
		g.mu.Lock()
		delete(g.waiters, provisional)
		delete(g.taskOf, provisional)
		if st := g.tasks[taskID]; st != nil {
			st.Phase = PhaseDiagnosing
			st.Pending = nil
			st.ApprovalID = ""
			if st.Failure == nil {
				delete(g.tasks, taskID)
			}
		}
		g.mu.Unlock()
		return Decision{Allow: false, Blocked: true, Message: "blocked: Auto Guard prompt failed: " + err.Error()}, err
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		g.mu.Lock()
		delete(g.waiters, provisional)
		delete(g.taskOf, provisional)
		if st := g.tasks[taskID]; st != nil {
			st.Phase = PhaseDiagnosing
			st.Pending = nil
			if st.Failure == nil {
				delete(g.tasks, taskID)
			}
		}
		g.mu.Unlock()
		return Decision{Allow: false, Blocked: true, Message: "blocked: Auto Guard prompt returned empty id"}, fmt.Errorf("empty Auto Guard approval id")
	}

	g.mu.Lock()
	// EmitPrompt implementations may bind the real id before emitting, which
	// lets a synchronous frontend resolve the card before EmitPrompt returns.
	// Only re-key a waiter that is still provisional; if both mappings are gone,
	// Resolve already completed and its buffered payload is waiting on reply.
	if provisionalReply, ok := g.waiters[provisional]; ok && provisionalReply != nil {
		delete(g.waiters, provisional)
		delete(g.taskOf, provisional)
		if existing, exists := g.waiters[approvalID]; exists && existing != nil {
			reply = existing
		} else {
			reply = provisionalReply
			g.waiters[approvalID] = reply
			g.taskOf[approvalID] = taskID
			st = g.taskLocked(taskID)
			st.ApprovalID = approvalID
		}
	} else if existing, ok := g.waiters[approvalID]; ok && existing != nil {
		reply = existing
	}
	g.mu.Unlock()
	g.persist()

	select {
	case payload := <-reply:
		return g.decisionFromResolve(payload)
	case <-ctx.Done():
		g.mu.Lock()
		delete(g.waiters, approvalID)
		delete(g.taskOf, approvalID)
		if st := g.tasks[taskID]; st != nil {
			st.Phase = PhaseDiagnosing
			st.Pending = nil
			st.ApprovalID = ""
			if st.Failure == nil {
				delete(g.tasks, taskID)
			}
		}
		g.mu.Unlock()
		g.persist()
		return Decision{Allow: false, Blocked: true, Message: "blocked: Auto Guard confirmation cancelled"}, ctx.Err()
	}
}

func (g *Gate) decisionFromResolve(payload resolvePayload) (Decision, error) {
	switch payload.action {
	case ActionContinue:
		return Decision{Allow: true}, nil
	case ActionRevise:
		msg := "blocked: user requested a revised Auto Guard action"
		if strings.TrimSpace(payload.feedback) != "" {
			msg += ": " + strings.TrimSpace(payload.feedback)
		}
		return Decision{Allow: false, Blocked: true, Message: msg}, nil
	default:
		return Decision{Allow: false, Blocked: true, Message: "blocked: unknown Auto Guard action"}, nil
	}
}

// RecordDiagnosis appends a diagnosis note while in diagnosing phase.
func (g *Gate) RecordDiagnosis(taskID, note string) {
	if g == nil || strings.TrimSpace(note) == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.tasks[normalizeTaskID(taskID)]
	if st == nil {
		return
	}
	if st.Failure == nil {
		return
	}
	if appendDiagnosisNoteLocked(st, note) {
		g.persistUnlocked()
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

func (g *Gate) recoveryGuidanceLocked(st *TaskState) string {
	if st.TailInjected {
		return ""
	}
	st.TailInjected = true
	return "Auto Guard is active after a tool/verification failure. " +
		"Diagnose with read-only tools first (read logs, search code, inspect the failing command output). " +
		"Before changing strategy, scope, or risk of the next write, explain the diagnosis and the proposed recovery step. " +
		"The host will block uncertain proposals with a reason and escalate repeated or high-risk changes automatically."
}

const (
	maxDiagnosisNotes     = 8
	maxDiagnosisNoteBytes = 800
)

func diagnosticObservationNote(obs Observation) string {
	tool := clip(strings.TrimSpace(obs.Tool), 120)
	if tool == "" {
		tool = "diagnostic"
	}
	subject := clip(firstNonEmpty(obs.Subject, ArgsSummary(obs.Args, 160)), 160)
	header := tool
	if subject != "" && subject != tool {
		header += " (" + subject + ")"
	}
	output := strings.TrimSpace(obs.Output)
	if output == "" {
		return clipDiagnosisNote(header + ": completed successfully")
	}
	return clipDiagnosisNote(header + ": " + output)
}

// appendDiagnosisNoteLocked deduplicates repeated reads and bounds both memory
// and the provider-visible reviewer request. Caller must hold g.mu.
func appendDiagnosisNoteLocked(st *TaskState, note string) bool {
	if st == nil || st.Failure == nil {
		return false
	}
	note = clipDiagnosisNote(note)
	if note == "" {
		return false
	}
	for _, existing := range st.Failure.DiagnosisNotes {
		if existing == note {
			return false
		}
	}
	st.Failure.DiagnosisNotes = append(st.Failure.DiagnosisNotes, note)
	if len(st.Failure.DiagnosisNotes) > maxDiagnosisNotes {
		st.Failure.DiagnosisNotes = st.Failure.DiagnosisNotes[len(st.Failure.DiagnosisNotes)-maxDiagnosisNotes:]
	}
	return true
}

func clipDiagnosisNote(note string) string {
	note = strings.TrimSpace(note)
	if len(note) <= maxDiagnosisNoteBytes {
		return note
	}
	const ellipsis = "…"
	cut := maxDiagnosisNoteBytes - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(note[cut]) {
		cut--
	}
	return note[:cut] + ellipsis
}

func (g *Gate) persist() {
	if g == nil || g.opts.Persist == nil {
		return
	}
	g.schedulePersist(g.Snapshot(), false)
}

func (g *Gate) persistUnlocked() {
	// Caller holds g.mu. Snapshot needs the lock — build inline.
	if g == nil || g.opts.Persist == nil {
		return
	}
	out := Snapshot{Tasks: map[string]*TaskState{}}
	for id, st := range g.tasks {
		if cp := cloneTaskState(st); cp != nil && !taskStateEmpty(cp) {
			out.Tasks[id] = cp
		}
	}
	// Persist outside the state lock to avoid deadlocks if Persist re-enters.
	g.schedulePersist(out, true)
}

func (g *Gate) schedulePersist(snap Snapshot, async bool) {
	if g == nil || g.opts.Persist == nil {
		return
	}
	key := ""
	if g.opts.PersistenceKey != nil {
		key = g.opts.PersistenceKey()
	}
	g.persistMu.Lock()
	g.persistSeq++
	seq := g.persistSeq
	g.persistPending[key]++
	g.persistMu.Unlock()
	write := func() {
		g.persistMu.Lock()
		defer g.persistMu.Unlock()
		defer func() {
			g.persistPending[key]--
			if g.persistPending[key] == 0 {
				delete(g.persistPending, key)
				delete(g.persistDone, key)
				g.persistCond.Broadcast()
			}
		}()
		if seq < g.persistDone[key] {
			return
		}
		g.opts.Persist(key, snap)
		g.persistDone[key] = seq
	}
	if async {
		go write()
		return
	}
	write()
}

func cloneTaskState(st *TaskState) *TaskState {
	if st == nil {
		return nil
	}
	cp := *st
	if st.Failure != nil {
		failure := *st.Failure
		failure.Args = append(json.RawMessage(nil), st.Failure.Args...)
		failure.DiagnosisNotes = append([]string(nil), st.Failure.DiagnosisNotes...)
		cp.Failure = &failure
	}
	if st.Pending != nil {
		pending := *st.Pending
		pending.Args = append(json.RawMessage(nil), st.Pending.Args...)
		cp.Pending = &pending
	}
	return &cp
}

func taskStateEmpty(st *TaskState) bool {
	return st == nil || (st.Failure == nil && st.Pending == nil && st.ApprovalID == "")
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
		return "the proposed mutation is high risk (destructive shell action, install, config, publish, or external write)"
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
	b.WriteString("blocked: Auto Guard requires human confirmation, but this environment has no decision channel.\n")
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

func (g *Gate) recordReviewBlock(taskID string, verdict ReviewVerdict) int {
	g.mu.Lock()
	st := g.taskLocked(taskID)
	st.ReviewBlocks++
	blocks := st.ReviewBlocks
	if st.Failure != nil {
		note := "Auto Guard reviewer blocked the proposal: " + firstNonEmpty(verdict.Rationale, verdict.Diagnosis)
		appendDiagnosisNoteLocked(st, note)
	}
	g.mu.Unlock()
	g.persist()
	return blocks
}

func reviewerBlockerMessage(verdict ReviewVerdict, attempt, limit int) string {
	reason := firstNonEmpty(verdict.Rationale, verdict.Diagnosis, "the action could not be verified as safe")
	return fmt.Sprintf(
		"blocked: Auto Guard reviewer could not verify this action (attempt %d/%d): %s. Diagnose further or propose a narrower same-strategy action before retrying.",
		attempt, limit, reason,
	)
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
		v.Rationale = "Auto Guard reviewer provided no rationale"
	}
	return v
}

// Ensure Gate implements agent.RecoveryGate.
var _ agent.RecoveryGate = (*Gate)(nil)
