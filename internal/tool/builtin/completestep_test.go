package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCompleteStepRejectsMissingEvidence(t *testing.T) {
	_, err := completeStep{}.Execute(context.Background(),
		json.RawMessage(`{"step":"Add the parser","result":"parser added","evidence":[]}`))
	if err == nil {
		t.Fatal("completion with empty evidence should be rejected")
	}
	if !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("error should mention evidence, got %v", err)
	}
}

func TestCompleteStepRequiresStepAndResult(t *testing.T) {
	cases := []string{
		`{"step":"","result":"x","evidence":[{"kind":"manual","summary":"checked"}]}`,
		`{"step":"x","result":"","evidence":[{"kind":"manual","summary":"checked"}]}`,
	}
	for _, c := range cases {
		if _, err := (completeStep{}).Execute(context.Background(), json.RawMessage(c)); err == nil {
			t.Fatalf("expected rejection for %s", c)
		}
	}
}

func TestCompleteStepRejectsBadEvidenceKind(t *testing.T) {
	_, err := completeStep{}.Execute(context.Background(),
		json.RawMessage(`{"step":"x","result":"y","evidence":[{"kind":"vibes","summary":"trust me"}]}`))
	if err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("bad evidence kind should be rejected, got %v", err)
	}
}

func TestCompleteStepRejectsEmptyEvidenceSummary(t *testing.T) {
	_, err := completeStep{}.Execute(context.Background(),
		json.RawMessage(`{"step":"x","result":"y","evidence":[{"kind":"verification","summary":""}]}`))
	if err == nil || !strings.Contains(err.Error(), "summary") {
		t.Fatalf("empty evidence summary should be rejected, got %v", err)
	}
}

func TestCompleteStepAccepts(t *testing.T) {
	out, err := completeStep{}.Execute(context.Background(), json.RawMessage(`{
		"step":"Add the parser",
		"result":"parser added and wired into the loop",
		"evidence":[
			{"kind":"verification","summary":"all tests pass","command":"go test ./..."},
			{"kind":"diff","summary":"new parser.go + call site","paths":["parser.go","loop.go"]}
		]}`))
	if err != nil {
		t.Fatalf("valid completion rejected: %v", err)
	}
	for _, want := range []string{"Add the parser", "2 evidence", "verification", "diff"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ack %q missing %q", out, want)
		}
	}
}

func TestCompleteStepReadOnly(t *testing.T) {
	if !(completeStep{}).ReadOnly() {
		t.Fatal("complete_step must be ReadOnly so it stays available and needs no approval")
	}
}
