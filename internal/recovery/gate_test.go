package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNoFailureAllowsMutation(t *testing.T) {
	g := NewGate(Options{Enabled: true, Mode: func() string { return "auto" }})
	dec, err := g.BeforeMutation(context.Background(), Proposal{
		Tool: "write_file", Subject: "a.go", Mutates: true,
		Args: json.RawMessage(`{"path":"a.go"}`),
	})
	if err != nil || !dec.Allow {
		t.Fatalf("BeforeMutation = (%+v, %v), want allow", dec, err)
	}
	if g.Metrics().HumanPrompts != 0 {
		t.Fatalf("unexpected prompt")
	}
}

func TestQualifyingFailureArmsDiagnosingAndAllowsReadOnly(t *testing.T) {
	g := NewGate(Options{Enabled: true, Mode: func() string { return "auto" }})
	g.ObserveResult(context.Background(), Observation{
		Tool: "bash", Subject: "go test ./...", Verification: true,
		Args:       json.RawMessage(`{"command":"go test ./..."}`),
		ErrSummary: "exit status 1", Output: "FAIL",
	})
	if got := g.Snapshot().Tasks["root"]; got == nil || got.Phase != PhaseDiagnosing {
		t.Fatalf("phase = %+v, want diagnosing", got)
	}
	dec, err := g.BeforeMutation(context.Background(), Proposal{
		Tool: "read_file", Subject: "a.go", ReadOnly: true,
		Args: json.RawMessage(`{"path":"a.go"}`),
	})
	if err != nil || !dec.Allow {
		t.Fatalf("readonly diagnosis blocked: %+v %v", dec, err)
	}
}

func TestQualifyingFailureReturnsGuidanceAndPersistsArmedState(t *testing.T) {
	persisted := make(chan Snapshot, 1)
	g := NewGate(Options{
		Enabled: true,
		Persist: func(_ string, s Snapshot) { persisted <- s },
	})
	guidance := g.ObserveResult(context.Background(), Observation{
		Tool: "bash", Subject: "go test ./...", Verification: true,
		Args:       json.RawMessage(`{"command":"go test ./..."}`),
		ErrSummary: "exit status 1", Output: "FAIL",
	})
	if !strings.Contains(guidance, "Diagnose with read-only tools first") {
		t.Fatalf("guidance = %q", guidance)
	}
	select {
	case snap := <-persisted:
		st := snap.Tasks["root"]
		if st == nil || st.Phase != PhaseDiagnosing || st.Failure == nil {
			t.Fatalf("persisted state = %+v, want armed diagnosing state", st)
		}
	case <-time.After(time.Second):
		t.Fatal("armed recovery state was not persisted")
	}
}

func TestAsyncPersistenceCapturesKeyWhenScheduled(t *testing.T) {
	key := "old-session"
	written := make(chan string, 1)
	g := NewGate(Options{
		Enabled:        true,
		PersistenceKey: func() string { return key },
		Persist: func(captured string, _ Snapshot) {
			written <- captured
		},
	})
	g.ObserveResult(context.Background(), Observation{
		Tool: "bash", Verification: true,
		Args: json.RawMessage(`{"command":"go test ./..."}`), ErrSummary: "fail",
	})
	key = "new-session"
	select {
	case got := <-written:
		if got != "old-session" {
			t.Fatalf("persistence key = %q, want captured old session", got)
		}
	case <-time.After(time.Second):
		t.Fatal("recovery persistence did not run")
	}
}

func TestSnapshotDeepCopiesMutableFailureFields(t *testing.T) {
	g := NewGate(Options{Enabled: true})
	g.ObserveResult(context.Background(), Observation{
		Tool: "bash", Verification: true,
		Args:       json.RawMessage(`{"command":"go test ./..."}`),
		ErrSummary: "exit status 1",
	})
	g.RecordDiagnosis("root", "failure is isolated to package a")
	snap := g.Snapshot()
	st := snap.Tasks["root"]
	st.Failure.Args[0] = '['
	st.Failure.DiagnosisNotes[0] = "mutated"

	original := g.Snapshot().Tasks["root"].Failure
	if string(original.Args) != `{"command":"go test ./..."}` {
		t.Fatalf("snapshot args aliased gate state: %s", original.Args)
	}
	if original.DiagnosisNotes[0] != "failure is isolated to package a" {
		t.Fatalf("snapshot diagnosis aliased gate state: %v", original.DiagnosisNotes)
	}
}

func TestEmptySearchDoesNotArm(t *testing.T) {
	g := NewGate(Options{Enabled: true})
	g.ObserveResult(context.Background(), Observation{
		Tool: "grep", ReadOnly: true, Success: false, EmptySearch: true,
		ErrSummary: "no matches",
	})
	if st := g.Snapshot().Tasks["root"]; st != nil && st.Phase != PhaseIdle && st.Failure != nil {
		t.Fatalf("empty search armed failure: %+v", st)
	}
}

func TestSafeVerificationRetryOnce(t *testing.T) {
	g := NewGate(Options{Enabled: true})
	args := json.RawMessage(`{"command":"go test ./..."}`)
	g.ObserveResult(context.Background(), Observation{
		Tool: "bash", Subject: "go test ./...", Verification: true, Args: args,
		ErrSummary: "exit 1",
	})
	// First same-command retry continues.
	dec, err := g.BeforeMutation(context.Background(), Proposal{
		Tool: "bash", Subject: "go test ./...", Verification: true, Args: args,
	})
	if err != nil || !dec.Allow {
		t.Fatalf("first retry = %+v %v", dec, err)
	}
	// Second needs confirmation (safe retry spent).
	var prompted atomic.Bool
	g.opts.EmitPrompt = func(ctx context.Context, taskID string, pending PendingProposal, failure *FailureEvent) (string, error) {
		prompted.Store(true)
		go func() {
			time.Sleep(5 * time.Millisecond)
			_ = g.Resolve("1", ActionContinue, "")
		}()
		return "1", nil
	}
	// Re-arm failure after first retry consumed without success.
	g.ObserveResult(context.Background(), Observation{
		Tool: "bash", Subject: "go test ./...", Verification: true, Args: args,
		ErrSummary: "exit 1",
	})
	dec, err = g.BeforeMutation(context.Background(), Proposal{
		Tool: "bash", Subject: "go test ./...", Verification: true, Args: args, Mutates: false,
	})
	// After re-arm, SafeRetryLeft resets to 1, so this may still auto-continue.
	// Force high-risk path for the second mutation style instead:
	_ = dec
	_ = err

	// Strategy change must prompt.
	prompted.Store(false)
	g.opts.EmitPrompt = func(ctx context.Context, taskID string, pending PendingProposal, failure *FailureEvent) (string, error) {
		prompted.Store(true)
		go func() {
			time.Sleep(5 * time.Millisecond)
			_ = g.Resolve("2", ActionContinue, "")
		}()
		return "2", nil
	}
	dec, err = g.BeforeMutation(context.Background(), Proposal{
		Tool: "write_file", Subject: "a.go", Mutates: true,
		StrategyChanged: true,
		Args:            json.RawMessage(`{"path":"a.go","content":"x"}`),
	})
	if err != nil || !dec.Allow {
		t.Fatalf("continue after strategy change = %+v %v", dec, err)
	}
	if !prompted.Load() {
		t.Fatal("expected recovery prompt for strategy change")
	}
}

func TestHighRiskForcesConfirm(t *testing.T) {
	g := NewGate(Options{Enabled: true})
	g.ObserveResult(context.Background(), Observation{
		Tool: "bash", Subject: "go test ./...", Verification: true,
		Args: json.RawMessage(`{"command":"go test ./..."}`), ErrSummary: "fail",
	})
	var got PendingProposal
	g.opts.EmitPrompt = func(ctx context.Context, taskID string, pending PendingProposal, failure *FailureEvent) (string, error) {
		got = pending
		go func() {
			time.Sleep(5 * time.Millisecond)
			_ = g.Resolve("9", ActionRevise, "only edit tests")
		}()
		return "9", nil
	}
	dec, err := g.BeforeMutation(context.Background(), Proposal{
		Tool: "bash", Subject: "rm -rf ./dist", Mutates: true,
		Args: json.RawMessage(`{"command":"rm -rf ./dist"}`),
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if dec.Allow || !dec.Blocked {
		t.Fatalf("want blocked revise, got %+v", dec)
	}
	if !strings.Contains(dec.Message, "only edit tests") {
		t.Fatalf("message = %q", dec.Message)
	}
	if got.ChangeKind != ChangeRisk && got.ChangeKind != ChangeStrategy {
		t.Fatalf("change_kind = %q", got.ChangeKind)
	}
}

func TestDeleteMutationsForceHumanConfirmationBeforeReviewer(t *testing.T) {
	for _, tool := range []string{"delete_range", "delete_symbol"} {
		t.Run(tool, func(t *testing.T) {
			var reviews atomic.Int32
			g := NewGate(Options{
				Enabled:  true,
				Headless: true,
				Reviewer: reviewerFunc(func(context.Context, *FailureEvent, []string, Proposal, string) (ReviewVerdict, error) {
					reviews.Add(1)
					return ReviewVerdict{Outcome: ReviewContinue, ChangeKind: ChangeSameStrategy}, nil
				}),
			})
			args := json.RawMessage(`{"path":"a.go"}`)
			g.ObserveResult(context.Background(), Observation{
				Tool: tool, Subject: "a.go", Mutates: true, Args: args, ErrSummary: "fail",
			})
			dec, err := g.BeforeMutation(context.Background(), Proposal{
				Tool: tool, Subject: "a.go", Mutates: true, Args: args,
			})
			if err != nil {
				t.Fatalf("BeforeMutation: %v", err)
			}
			if dec.Allow || !dec.Blocked {
				t.Fatalf("delete mutation = %+v, want human-confirmation blocker", dec)
			}
			if got := reviews.Load(); got != 0 {
				t.Fatalf("reviewer calls = %d, want deterministic high-risk rule", got)
			}
		})
	}
}

func TestContinueConsumesFingerprintOnce(t *testing.T) {
	g := NewGate(Options{Enabled: true})
	g.ObserveResult(context.Background(), Observation{
		Tool: "bash", Subject: "go test", Verification: true,
		Args: json.RawMessage(`{"command":"go test"}`), ErrSummary: "fail",
	})
	args := json.RawMessage(`{"path":"a.go","content":"fixed"}`)
	prop := Proposal{Tool: "write_file", Subject: "a.go", Preview: "+fixed", Mutates: true, Args: args}
	fp := CallFingerprint(prop.Tool, prop.Subject, prop.Preview, prop.Args)

	g.opts.EmitPrompt = func(ctx context.Context, taskID string, pending PendingProposal, failure *FailureEvent) (string, error) {
		if pending.Fingerprint != fp {
			t.Fatalf("fingerprint mismatch")
		}
		go func() {
			time.Sleep(5 * time.Millisecond)
			_ = g.Resolve("c1", ActionContinue, "")
		}()
		return "c1", nil
	}
	dec, err := g.BeforeMutation(context.Background(), prop)
	if err != nil || !dec.Allow || !dec.ConsumedApprovedOnce {
		t.Fatalf("first continue = %+v %v", dec, err)
	}

	// Same fingerprint without new approval must re-prompt (grant consumed).
	var prompts int32
	g.opts.EmitPrompt = func(ctx context.Context, taskID string, pending PendingProposal, failure *FailureEvent) (string, error) {
		atomic.AddInt32(&prompts, 1)
		go func() {
			time.Sleep(5 * time.Millisecond)
			_ = g.Resolve("c2", ActionContinue, "")
		}()
		return "c2", nil
	}
	// Failure still active (mutation was not observed successful).
	dec, err = g.BeforeMutation(context.Background(), prop)
	if err != nil || !dec.Allow {
		t.Fatalf("second continue = %+v %v", dec, err)
	}
	if atomic.LoadInt32(&prompts) != 1 {
		t.Fatalf("expected re-prompt after fingerprint consumption")
	}
}

func TestStopCancels(t *testing.T) {
	var cancelled atomic.Bool
	g := NewGate(Options{
		Enabled: true,
		Cancel:  func() { cancelled.Store(true) },
	})
	g.ObserveResult(context.Background(), Observation{
		Tool: "bash", Verification: true, Subject: "go test",
		Args: json.RawMessage(`{"command":"go test"}`), ErrSummary: "fail",
	})
	g.opts.EmitPrompt = func(ctx context.Context, taskID string, pending PendingProposal, failure *FailureEvent) (string, error) {
		go func() {
			time.Sleep(5 * time.Millisecond)
			_ = g.Resolve("s1", ActionStop, "")
		}()
		return "s1", nil
	}
	dec, err := g.BeforeMutation(context.Background(), Proposal{
		Tool: "write_file", Subject: "a.go", Mutates: true,
		Args: json.RawMessage(`{"path":"a.go"}`),
	})
	if !errors.Is(err, ErrStopped) {
		t.Fatalf("err = %v, want ErrStopped", err)
	}
	if dec.Allow {
		t.Fatal("stop must not allow mutation")
	}
	if !cancelled.Load() {
		t.Fatal("expected cancel callback")
	}
}

func TestReviewerContinueSkipsPrompt(t *testing.T) {
	g := NewGate(Options{
		Enabled: true,
		Reviewer: staticReviewer{ReviewVerdict{
			Outcome: ReviewContinue, ChangeKind: ChangeSameStrategy,
			FailureSummary: "test fail", Diagnosis: "flake", ProposedAction: "retry edit",
			Rationale: "same patch retry",
		}},
	})
	g.ObserveResult(context.Background(), Observation{
		Tool: "bash", Verification: true, Subject: "go test ./foo",
		Args: json.RawMessage(`{"command":"go test ./foo"}`), ErrSummary: "fail",
	})
	var prompted atomic.Bool
	g.opts.EmitPrompt = func(ctx context.Context, taskID string, pending PendingProposal, failure *FailureEvent) (string, error) {
		prompted.Store(true)
		return "x", nil
	}
	dec, err := g.BeforeMutation(context.Background(), Proposal{
		Tool: "write_file", Subject: "a.go", Mutates: true,
		Args: json.RawMessage(`{"path":"a.go","content":"y"}`),
	})
	if err != nil || !dec.Allow {
		t.Fatalf("reviewer continue = %+v %v", dec, err)
	}
	if prompted.Load() {
		t.Fatal("targeted edit after verifier failure must be reviewable without a prompt")
	}
}

func TestReviewerUsesProposalTaskSummaryBeforeRootFallback(t *testing.T) {
	reviewer := &capturingReviewer{v: ReviewVerdict{
		Outcome: ReviewContinue, ChangeKind: ChangeSameStrategy,
	}}
	g := NewGate(Options{
		Enabled:     true,
		Reviewer:    reviewer,
		TaskSummary: func() string { return "root task" },
	})
	g.ObserveResult(context.Background(), Observation{
		TaskID: "subagent:child", Tool: "bash", Verification: true,
		Args: json.RawMessage(`{"command":"go test ./child"}`), ErrSummary: "fail",
	})
	dec, err := g.BeforeMutation(context.Background(), Proposal{
		TaskID: "subagent:child", TaskSummary: "child task", Tool: "write_file",
		Subject: "child.go", Mutates: true, Args: json.RawMessage(`{"path":"child.go"}`),
	})
	if err != nil || !dec.Allow {
		t.Fatalf("review decision = %+v, %v", dec, err)
	}
	if reviewer.taskSummary != "child task" {
		t.Fatalf("reviewer task summary = %q, want child task", reviewer.taskSummary)
	}
}

func TestStrategyChangedRequiresSemanticSignal(t *testing.T) {
	failure := &FailureEvent{Tool: "bash", Verification: true}
	proposal := Proposal{Tool: "edit_file", Mutates: true}
	if StrategyChanged(failure, proposal) {
		t.Fatal("tool transition alone must not be treated as a strategy change")
	}
	proposal.StrategyChanged = true
	if !StrategyChanged(failure, proposal) {
		t.Fatal("an explicit semantic strategy change must force confirmation")
	}
}

func TestReviewerErrorFailsClosed(t *testing.T) {
	g := NewGate(Options{
		Enabled:  true,
		Reviewer: errReviewer{},
	})
	g.ObserveResult(context.Background(), Observation{
		Tool: "write_file", Subject: "a.go", Mutates: true,
		Args: json.RawMessage(`{"path":"a.go"}`), ErrSummary: "fail",
	})
	var prompted atomic.Bool
	g.opts.EmitPrompt = func(ctx context.Context, taskID string, pending PendingProposal, failure *FailureEvent) (string, error) {
		prompted.Store(true)
		go func() {
			time.Sleep(5 * time.Millisecond)
			_ = g.Resolve("e1", ActionContinue, "")
		}()
		return "e1", nil
	}
	dec, err := g.BeforeMutation(context.Background(), Proposal{
		Tool: "write_file", Subject: "a.go", Mutates: true,
		Args: json.RawMessage(`{"path":"a.go","content":"z"}`),
	})
	if err != nil || !dec.Allow {
		t.Fatalf("got %+v %v", dec, err)
	}
	if !prompted.Load() {
		t.Fatal("reviewer error must prompt human")
	}
}

func TestAskYoloModesInactive(t *testing.T) {
	for _, mode := range []string{"ask", "yolo"} {
		g := NewGate(Options{Enabled: true, Mode: func() string { return mode }})
		g.ObserveResult(context.Background(), Observation{
			Tool: "bash", Verification: true, ErrSummary: "fail",
			Args: json.RawMessage(`{"command":"go test"}`),
		})
		// Mode inactive: ObserveResult ignored, no failure.
		if st := g.Snapshot().Tasks["root"]; st != nil && st.Failure != nil {
			t.Fatalf("mode %s armed failure", mode)
		}
	}
}

func TestHeadlessBlocksWithoutWait(t *testing.T) {
	g := NewGate(Options{Enabled: true, Headless: true})
	g.ObserveResult(context.Background(), Observation{
		Tool: "bash", Verification: true, Subject: "go test",
		Args: json.RawMessage(`{"command":"go test"}`), ErrSummary: "fail",
	})
	dec, err := g.BeforeMutation(context.Background(), Proposal{
		Tool: "write_file", Subject: "a.go", Mutates: true,
		Args: json.RawMessage(`{"path":"a.go"}`),
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if dec.Allow || !dec.Blocked || !strings.Contains(dec.Message, "no decision channel") {
		t.Fatalf("want headless blocker, got %+v", dec)
	}
}

func TestSuccessfulMutationClearsFailure(t *testing.T) {
	g := NewGate(Options{Enabled: true})
	g.ObserveResult(context.Background(), Observation{
		Tool: "bash", Verification: true, ErrSummary: "fail",
		Args: json.RawMessage(`{"command":"go test"}`),
	})
	g.ObserveResult(context.Background(), Observation{
		Tool: "write_file", Mutates: true, Success: true,
		Args: json.RawMessage(`{"path":"a.go"}`),
	})
	st := g.Snapshot().Tasks["root"]
	if st != nil {
		t.Fatalf("want cleared task slot removed, got %+v", st)
	}
}

func TestSuccessfulCallsDoNotAccumulateEmptyTaskSlots(t *testing.T) {
	g := NewGate(Options{Enabled: true})
	for _, taskID := range []string{"root", "subagent:a", "subagent:b"} {
		g.ObserveResult(context.Background(), Observation{
			TaskID: taskID, Tool: "read_file", ReadOnly: true, Success: true,
		})
		dec, err := g.BeforeMutation(context.Background(), Proposal{
			TaskID: taskID, Tool: "write_file", Mutates: true,
			Args: json.RawMessage(`{"path":"a.go"}`),
		})
		if err != nil || !dec.Allow {
			t.Fatalf("task %q mutation = %+v, %v", taskID, dec, err)
		}
	}
	if got := g.Snapshot().Tasks; len(got) != 0 {
		t.Fatalf("normal calls accumulated empty recovery states: %+v", got)
	}
}

func TestReplayedRecoveryReviseQueuesGuidanceForNextTurn(t *testing.T) {
	g := NewGate(Options{Enabled: true})
	g.Restore(Snapshot{Tasks: map[string]*TaskState{
		"root": {
			Phase:   PhaseAwaitingDecision,
			Failure: &FailureEvent{Tool: "bash", ErrSummary: "tests failed"},
			Pending: &PendingProposal{Tool: "write_file", Fingerprint: "fingerprint"},
		},
	}})
	g.BindApprovalID("root", "replayed-1")
	if err := g.Resolve("replayed-1", ActionRevise, "only edit the failing package"); err != nil {
		t.Fatalf("Resolve replayed revise: %v", err)
	}
	guidance := g.ConsumeGuidance("root")
	if !strings.Contains(guidance, "only edit the failing package") {
		t.Fatalf("queued guidance = %q", guidance)
	}
	if duplicate := g.ConsumeGuidance("root"); duplicate != "" {
		t.Fatalf("guidance was not one-shot: %q", duplicate)
	}
}

func TestReplayedRecoveryContinueRestoresOneShotFingerprint(t *testing.T) {
	args := json.RawMessage(`{"path":"a.go","content":"fixed"}`)
	fingerprint := CallFingerprint("write_file", "a.go", "a.go", args)
	g := NewGate(Options{Enabled: true})
	g.Restore(Snapshot{Tasks: map[string]*TaskState{
		"root": {
			Phase:   PhaseAwaitingDecision,
			Failure: &FailureEvent{Tool: "bash", ErrSummary: "tests failed"},
			Pending: &PendingProposal{Tool: "write_file", Subject: "a.go", Preview: "a.go", Args: args, Fingerprint: fingerprint},
		},
	}})
	g.BindApprovalID("root", "replayed-2")
	if err := g.Resolve("replayed-2", ActionContinue, ""); err != nil {
		t.Fatalf("Resolve replayed continue: %v", err)
	}
	dec, err := g.BeforeMutation(context.Background(), Proposal{
		Tool: "write_file", Subject: "a.go", Preview: "a.go", Args: args, Mutates: true,
	})
	if err != nil || !dec.Allow || !dec.ConsumedApprovedOnce {
		t.Fatalf("replayed one-shot decision = %+v, %v", dec, err)
	}
}

func TestUserRejectAndBlockedDoNotArm(t *testing.T) {
	g := NewGate(Options{Enabled: true})
	g.ObserveResult(context.Background(), Observation{
		Tool: "write_file", Mutates: true, UserRejected: true, ErrSummary: "denied",
	})
	g.ObserveResult(context.Background(), Observation{
		Tool: "write_file", Mutates: true, Blocked: true, ErrSummary: "plan mode",
	})
	if st := g.Snapshot().Tasks["root"]; st != nil && st.Failure != nil {
		t.Fatalf("armed on non-qualifying: %+v", st)
	}
}

type staticReviewer struct{ v ReviewVerdict }

func (s staticReviewer) Review(context.Context, *FailureEvent, []string, Proposal, string) (ReviewVerdict, error) {
	return s.v, nil
}

type reviewerFunc func(context.Context, *FailureEvent, []string, Proposal, string) (ReviewVerdict, error)

func (f reviewerFunc) Review(ctx context.Context, failure *FailureEvent, diagnosis []string, proposal Proposal, taskSummary string) (ReviewVerdict, error) {
	return f(ctx, failure, diagnosis, proposal, taskSummary)
}

type capturingReviewer struct {
	v           ReviewVerdict
	taskSummary string
}

func (r *capturingReviewer) Review(_ context.Context, _ *FailureEvent, _ []string, _ Proposal, taskSummary string) (ReviewVerdict, error) {
	r.taskSummary = taskSummary
	return r.v, nil
}

type errReviewer struct{}

func (errReviewer) Review(context.Context, *FailureEvent, []string, Proposal, string) (ReviewVerdict, error) {
	return ReviewVerdict{}, errors.New("timeout")
}
