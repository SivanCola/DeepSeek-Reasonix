package control

import (
	"context"
	"errors"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func readinessContinuationController(t *testing.T, turns [][]provider.Chunk, sink event.Sink) (*Controller, *scriptedTurns) {
	t.Helper()
	reg := tool.NewRegistry()
	reg.Add(minimalFakeTool{name: "write_file"})
	reg.Add(minimalFakeTool{name: "read_file", readOnly: true})
	reg.Add(minimalFakeTool{name: "bash"})
	prov := &scriptedTurns{turns: turns}
	executor := agent.New(prov, reg, agent.NewSession(""), agent.Options{}, event.Discard)
	c := New(Options{Runner: executor, Executor: executor, Sink: sink})
	t.Cleanup(c.Close)
	return c, prov
}

func TestOrdinaryTurnAutomaticallyFinishesKnownChecks(t *testing.T) {
	notices := 0
	c, prov := readinessContinuationController(t, [][]provider.Chunk{
		{toolCallChunk("write", "write_file", `{"path":"main.go","content":"package main"}`), {Type: provider.ChunkDone}},
		textTurn("implemented"),
		{toolCallChunk("verify", "bash", `{"command":"go test ./..."}`), {Type: provider.ChunkDone}},
		{toolCallChunk("review", "read_file", `{"path":"main.go"}`), {Type: provider.ChunkDone}},
		textTurn("implemented and verified"),
	}, event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Text != "" {
			notices++
		}
	}))

	if err := newTurnOrchestrator(c).runGoalLoopWithRawDisplay(context.Background(), "update main.go", "update main.go", ""); err != nil {
		t.Fatalf("ordinary turn returned a readiness failure: %v", err)
	}
	if prov.call < 5 {
		t.Fatalf("provider calls = %d, want the host to continue through verification and review", prov.call)
	}
	if notices == 0 {
		t.Fatal("automatic readiness continuation did not emit a progress notice")
	}
	if c.executor.PrepareFinalReadinessRecovery() {
		t.Fatal("successful automatic continuation left a manual recovery action pending")
	}
}

func TestReadinessContinuationPromptStaysHiddenFromUserHistory(t *testing.T) {
	prompt := readinessContinuationPrompt(nil, "run the missing verification")
	if !IsSyntheticUserMessage(prompt) {
		t.Fatalf("readiness continuation was treated as user-authored text: %q", prompt)
	}
}

func TestOrdinaryReadinessContinuationStopsAfterStalledGap(t *testing.T) {
	c, prov := readinessContinuationController(t, [][]provider.Chunk{
		{toolCallChunk("write", "write_file", `{"path":"main.go","content":"package main"}`), {Type: provider.ChunkDone}},
		textTurn("implemented without checks"),
	}, event.Discard)

	err := newTurnOrchestrator(c).runGoalLoopWithRawDisplay(context.Background(), "update main.go", "update main.go", "")
	var readinessErr *agent.FinalReadinessError
	if !errors.As(err, &readinessErr) {
		t.Fatalf("stalled continuation error = %v, want final readiness failure", err)
	}
	if prov.call > 4 {
		t.Fatalf("provider calls = %d, want the unchanged gap bounded after two retries", prov.call)
	}
}

func TestEditedOrdinaryTurnAlsoFinishesKnownChecks(t *testing.T) {
	c, prov := readinessContinuationController(t, [][]provider.Chunk{
		{toolCallChunk("write", "write_file", `{"path":"main.go","content":"package main"}`), {Type: provider.ChunkDone}},
		textTurn("implemented"),
		{toolCallChunk("verify", "bash", `{"command":"go test ./..."}`), {Type: provider.ChunkDone}},
		{toolCallChunk("review", "read_file", `{"path":"main.go"}`), {Type: provider.ChunkDone}},
		textTurn("implemented and verified"),
	}, event.Discard)

	err := newTurnOrchestrator(c).runEditedGoalLoopWithRawDisplay(
		context.Background(), "update main.go", "update main.go", "", "old request",
	)
	if err != nil {
		t.Fatalf("edited ordinary turn returned a readiness failure: %v", err)
	}
	if prov.call < 5 {
		t.Fatalf("provider calls = %d, want edited turns to share automatic readiness continuation", prov.call)
	}
}
