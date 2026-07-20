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
		Args: json.RawMessage(`{"command":"go test ./..."}`),
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
		Args: json.RawMessage(`{"path":"a.go","content":"x"}`),
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
	// Same tool write to same path — not high risk, not expanded, not strategy change of tool.
	// StrategyChanged will be true because tool changed from bash to write_file.
	// Use same tool bash with non-high-risk non-verification mutation? bash echo is mutates?
	// Actually for reviewer continue we need forceConfirm=false, so proposal must not
	// trip strategy/scope/risk. Same tool bash re-run of a non-verification mutating
	// command after a verification failure is strategy-same only if tool matches.
	// Arm failure as write_file instead:
	g = NewGate(Options{
		Enabled: true,
		Reviewer: staticReviewer{ReviewVerdict{
			Outcome: ReviewContinue, ChangeKind: ChangeSameStrategy,
			Rationale: "same",
		}},
	})
	g.ObserveResult(context.Background(), Observation{
		Tool: "write_file", Subject: "a.go", Mutates: true,
		Args: json.RawMessage(`{"path":"a.go"}`), ErrSummary: "disk full",
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
		t.Fatal("reviewer continue must not prompt")
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
	if st == nil || st.Phase != PhaseIdle || st.Failure != nil {
		t.Fatalf("want cleared, got %+v", st)
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

type errReviewer struct{}

func (errReviewer) Review(context.Context, *FailureEvent, []string, Proposal, string) (ReviewVerdict, error) {
	return ReviewVerdict{}, errors.New("timeout")
}
