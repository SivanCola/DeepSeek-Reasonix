package agent

import (
	"context"
	"encoding/json"
	"testing"
)

type recordingRecoveryGate struct {
	observation RecoveryObservation
}

func (g *recordingRecoveryGate) ObserveResult(_ context.Context, observation RecoveryObservation) string {
	g.observation = observation
	return ""
}

func (*recordingRecoveryGate) BeforeMutation(context.Context, RecoveryProposal) (RecoveryDecision, error) {
	return RecoveryDecision{Allow: true}, nil
}

func TestObserveRecoveryResultMarksCancellation(t *testing.T) {
	gate := &recordingRecoveryGate{}
	a := &Agent{recoveryGate: gate}
	a.observeRecoveryResult(
		context.Background(),
		"write_file",
		json.RawMessage(`{"path":"a.go"}`),
		false,
		true,
		"cancelled",
		context.Canceled,
		false,
		false,
	)
	if !gate.observation.Cancelled {
		t.Fatalf("observation = %+v, want cancellation marked", gate.observation)
	}
}
