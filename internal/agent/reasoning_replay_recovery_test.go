package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

type strictNoWarningReasoningProvider struct{ *testutil.MockProvider }

func (p strictNoWarningReasoningProvider) RequiresToolCallReasoning() bool      { return true }
func (p strictNoWarningReasoningProvider) WarnOnMissingToolCallReasoning() bool { return false }

func TestStrictMissingReasoningRetryIsNotSuppressedAcrossRuns(t *testing.T) {
	stateDir := t.TempDir()
	call := provider.ToolCall{ID: "c1", Name: "echo", Arguments: `{"text":"hi"}`}
	seedProvider := strictAssistantReasoningProvider{testutil.NewMock("strict-replay")}
	fingerprint := provider.MissingToolCallReasoningWarningFingerprint(seedProvider)
	if !newMissingReasoningWarnState(stateDir).claim(fingerprint) {
		t.Fatal("failed to seed a persisted incident from the previous behavior")
	}

	firstProvider := testutil.NewMock("strict-replay",
		testutil.Turn{ToolCalls: []provider.ToolCall{call}},
		testutil.Turn{ToolCalls: []provider.ToolCall{call}},
	)
	firstSink := &recordSink{}
	first := New(strictAssistantReasoningProvider{firstProvider}, echoRegistry(), NewSession(""), Options{
		MissingReasoningWarnStateDir: stateDir,
	}, firstSink)
	var replayErr *ReasoningReplayError
	if err := first.Run(withNoClosedLoop(context.Background()), "go"); !errors.As(err, &replayErr) {
		t.Fatalf("first Run error = %v, want ReasoningReplayError", err)
	}
	if message := replayErr.Error(); !strings.Contains(message, "after an automatic retry") || !strings.Contains(message, "fresh attempt") {
		t.Fatalf("ReasoningReplayError = %q, want attempted recovery and actionable retry guidance", message)
	}
	if got := firstSink.recoveryCount(event.ProtocolRecoveryMissingReasoningRetryAttempted); got != 1 {
		t.Fatalf("first Run retries = %d, want 1", got)
	}

	secondProvider := testutil.NewMock("strict-replay",
		testutil.Turn{ToolCalls: []provider.ToolCall{call}},
		testutil.Turn{Reasoning: "safe retry", ToolCalls: []provider.ToolCall{call}},
		testutil.Turn{Text: "done"},
	)
	secondSink := &recordSink{}
	second := New(strictAssistantReasoningProvider{secondProvider}, echoRegistry(), NewSession(""), Options{
		MissingReasoningWarnStateDir: stateDir,
	}, secondSink)
	if err := second.Run(withNoClosedLoop(context.Background()), "go"); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if got := secondSink.recoveryCount(event.ProtocolRecoveryMissingReasoningRetryAttempted); got != 1 {
		t.Fatalf("second Run retries = %d, want 1 despite the persisted incident", got)
	}
	requests := secondProvider.Requests()
	if len(requests) != 3 || !reflect.DeepEqual(requests[0], requests[1]) {
		t.Fatalf("strict recovery requests = %d, want identical retry followed by final turn", len(requests))
	}
}

func TestStrictNoWarningReplayDoesNotLeakSpeculativeEvents(t *testing.T) {
	missingTurn := func(id string) testutil.Turn {
		call := provider.ToolCall{ID: id, Name: "echo", Arguments: `{"text":"must not run"}`}
		return testutil.Turn{Chunks: []provider.Chunk{
			{Type: provider.ChunkText, Text: "speculative"},
			{Type: provider.ChunkToolCallStart, ToolCall: &call},
			{Type: provider.ChunkToolCall, ToolCall: &call},
			{Type: provider.ChunkDone},
		}}
	}
	providerMock := testutil.NewMock("strict-no-warning", missingTurn("c1"), missingTurn("c2"))
	sink := &recordSink{}
	agent := New(strictNoWarningReasoningProvider{providerMock}, echoRegistry(), NewSession(""), Options{}, sink)

	var replayErr *ReasoningReplayError
	if err := agent.Run(withNoClosedLoop(context.Background()), "go"); !errors.As(err, &replayErr) {
		t.Fatalf("Run error = %v, want ReasoningReplayError", err)
	}
	if got := providerMock.CallCount(); got != 2 {
		t.Fatalf("provider calls = %d, want malformed turn plus one exact retry", got)
	}
	for _, kind := range []event.Kind{event.ToolDispatch, event.ToolResult, event.Text, event.Message} {
		if got := len(sink.kinds(kind)); got != 0 {
			t.Fatalf("speculative %v events = %d, want 0", kind, got)
		}
	}
}
