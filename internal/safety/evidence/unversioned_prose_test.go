package evidence

import "testing"

func TestDeliveryOwesNoGenericCheckForProseInAnUnversionedWorkspace(t *testing.T) {
	root := t.TempDir()
	doc := Receipt{ToolName: "write_file", Success: true, Mutation: true, Write: true, MutationEvidence: MutationProven, Paths: []string{"docs/a.md"}}
	code := Receipt{ToolName: "write_file", Success: true, Mutation: true, Write: true, MutationEvidence: MutationProven, Paths: []string{"main.go"}}
	unscoped := Receipt{ToolName: "bash", Command: "./gen.sh", Success: true, Mutation: true, MutationEvidence: MutationUnknown}
	base := CaptureCheckContract(nil, nil).WithObserveRoot(root).WithDelivery(true)

	for _, tc := range []struct {
		name     string
		contract CheckContract
		receipts []Receipt
		waived   bool
	}{
		{"unversioned, delivery, named prose", base.WithUnversionedWorkspace(true), []Receipt{doc}, true},
		{"inside a repository", base, []Receipt{doc}, false},
		{"declared check", CaptureCheckContract(nil, []string{"make test"}).WithObserveRoot(root).WithDelivery(true).WithUnversionedWorkspace(true), []Receipt{doc}, false},
		{"captured criterion", base.WithCapturedTests(1).WithUnversionedWorkspace(true), []Receipt{doc}, false},
		{"a change beyond prose", base.WithUnversionedWorkspace(true), []Receipt{doc, code}, false},
		{"a change whose extent is unestablished", base.WithUnversionedWorkspace(true), []Receipt{doc, unscoped}, false},
		{"an unseen writer", base.WithUnversionedWorkspace(true).WithUnseenWriter(true), []Receipt{doc}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := ledgerOf(tc.receipts...)
			if got := l.ProseOnlyWithoutChecks(tc.contract); got != tc.waived {
				t.Errorf("ProseOnlyWithoutChecks = %v, want %v", got, tc.waived)
			}
			stale := false
			for _, o := range l.Obligations(tc.contract) {
				stale = stale || o.Kind == ObligationStaleVerification
			}
			if stale == tc.waived {
				t.Errorf("stale verification owed = %v, want %v", stale, !tc.waived)
			}
		})
	}
}

// A command the shell grammar cannot read is unknown, and only observing the
// workspace settles it; a read-only git query is none whatever its exit status.
func TestWhichCommandsLeaveAnExtentOnlyObservationCanSettle(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    string
	}{
		{"$repo='D:\\x'; foreach($d in 'a','b'){ git -C $repo status; Write-Host $d }", MutationUnknown},
		{"Test-Path docs/a.md", MutationUnknown},
		{"git rev-parse --show-toplevel", MutationNone},
		{"git status", MutationNone},
		{"Get-Content a.md", MutationNone},
	} {
		if got := bashMutationClass(tc.command); got != tc.want {
			t.Errorf("%q is %s, want %s", tc.command, got, tc.want)
		}
	}
}
