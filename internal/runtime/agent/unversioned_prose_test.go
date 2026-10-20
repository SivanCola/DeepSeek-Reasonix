package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/contract/tool"
	"reasonix/internal/runtime/taskpolicy"
	"reasonix/internal/safety/evidence"
	"reasonix/internal/state/instruction"
)

func TestDeliveryAsksNoVerificationForProseInAnUnversionedWorkspace(t *testing.T) {
	write := func(path string) evidence.Receipt {
		return evidence.Receipt{ToolName: "write_file", Success: true, Mutation: true, Write: true, MutationEvidence: evidence.MutationProven, Paths: []string{path}}
	}
	unscoped := evidence.Receipt{ToolName: "bash", Command: "./gen.sh", Success: true, Mutation: true, MutationEvidence: evidence.MutationUnknown}
	for _, tc := range []struct {
		name        string
		unversioned bool
		declared    bool
		receipts    []evidence.Receipt
		wantMissing int
	}{
		{name: "inside a repository", receipts: []evidence.Receipt{write("notes.md")}, wantMissing: 1},
		{name: "unversioned, prose only", unversioned: true, receipts: []evidence.Receipt{write("notes.md")}},
		{name: "unversioned, declared check", unversioned: true, declared: true, receipts: []evidence.Receipt{write("notes.md")}, wantMissing: 1},
		{name: "unversioned, code beyond prose", unversioned: true, receipts: []evidence.Receipt{write("notes.md"), write("main.go")}, wantMissing: 1},
		{name: "unversioned, extent unestablished", unversioned: true, receipts: []evidence.Receipt{write("notes.md"), unscoped}, wantMissing: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := tool.NewRegistry()
			reg.Add(fakeTool{name: "bash"})
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("A neutral note."), 0o600); err != nil {
				t.Fatal(err)
			}
			a := &Agent{
				task: taskRuntime{ledger: readinessLedger(tc.receipts...)},
				svc:  agentServices{tools: reg},
				turn: turnRuntime{policySet: true, policy: taskpolicy.TaskPolicy{Verification: taskpolicy.VerifyTargeted}},
			}
			a.observeRoot, a.writeWorkspaceRoot = root, root
			a.deliveryProfile = true
			a.workspaceUnversioned = tc.unversioned
			if tc.declared {
				a.projectChecks = []instruction.VerifyCheck{{Command: "make test"}}
			}
			got := a.finalReadinessCheckFor().missingVerification
			if (got > 0) != (tc.wantMissing > 0) {
				t.Errorf("missing verification = %d, want %d", got, tc.wantMissing)
			}
		})
	}
}

func TestAVerificationDebtIsExplainedOnceThenReferredTo(t *testing.T) {
	code := func(path string) evidence.Receipt {
		return evidence.Receipt{ToolName: "write_file", Success: true, Mutation: true, Write: true, MutationEvidence: evidence.MutationProven, Paths: []string{path}}
	}
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "bash"})
	a := &Agent{
		task: taskRuntime{ledger: readinessLedger(code("main.go"))},
		svc:  agentServices{tools: reg},
		turn: turnRuntime{policySet: true, policy: taskpolicy.TaskPolicy{Verification: taskpolicy.VerifyTargeted}},
	}
	a.observeRoot = t.TempDir()

	first, owed := a.verificationGap(0)
	if !owed || !strings.Contains(first, "for the current role setting") {
		t.Fatalf("first = %q, want the full explanation", first)
	}
	if again, _ := a.verificationGap(0); again != first {
		t.Fatalf("asking twice without ending the turn must not shorten it: %q", again)
	}
	a.noteObligationsExplained()
	second, owed := a.verificationGap(0)
	if !owed || second == first || !strings.Contains(second, "still owed") || !strings.Contains(second, "verification command") {
		t.Fatalf("second = %q, want the short reference that still names what to run", second)
	}
	a.task.ledger.Record(code("other.go"))
	if again, _ := a.verificationGap(0); !strings.Contains(again, "for the current role setting") {
		t.Fatalf("a later change is a new debt and is explained in full: %q", again)
	}
}

// Whether a round moved is read off what is owed, never off how much of it the
// round spells out: a debt shortened on the second round is the same debt, and
// a changed signature there would count as progress and buy the loop another round.
func TestAbridgingADebtDoesNotChangeTheProgressSignature(t *testing.T) {
	code := evidence.Receipt{ToolName: "write_file", Success: true, Mutation: true, Write: true, MutationEvidence: evidence.MutationProven, Paths: []string{"main.go"}}
	unproven := evidence.Receipt{ToolName: "bash", Success: true, Write: true, Mutation: true, MutationEvidence: evidence.MutationUnknown, Command: "./gen.sh"}
	for _, tc := range []struct {
		name     string
		delivery bool
		receipts []evidence.Receipt
	}{
		{name: "verification debt", receipts: []evidence.Receipt{code}},
		{name: "unproven change under delivery", delivery: true, receipts: []evidence.Receipt{unproven}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := tool.NewRegistry()
			reg.Add(fakeTool{name: "bash"})
			a := &Agent{
				task: taskRuntime{ledger: readinessLedger(tc.receipts...)},
				svc:  agentServices{tools: reg},
				turn: turnRuntime{policySet: true, policy: taskpolicy.TaskPolicy{Verification: taskpolicy.VerifyTargeted}},
			}
			a.observeRoot = t.TempDir()
			a.deliveryProfile = tc.delivery
			first := a.finalReadinessCheckFor()
			a.noteObligationsExplained()
			second := a.finalReadinessCheckFor()
			if first.reason == second.reason {
				t.Fatalf("the second round was not abridged: %q", second.reason)
			}
			if first.progressSignature() != second.progressSignature() {
				t.Errorf("signature moved when the same debt was merely shortened:\n%q\n%q", first.progressSignature(), second.progressSignature())
			}
		})
	}
}
