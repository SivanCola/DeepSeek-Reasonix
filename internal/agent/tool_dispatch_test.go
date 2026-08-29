package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"reasonix/internal/evidence"
	"reasonix/internal/tool"
)

type transientOnceTool struct {
	calls atomic.Int32
}

func (t *transientOnceTool) Name() string            { return "read_file" }
func (t *transientOnceTool) Description() string     { return "" }
func (t *transientOnceTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *transientOnceTool) ReadOnly() bool          { return true }
func (t *transientOnceTool) Execute(context.Context, json.RawMessage) (string, error) {
	if t.calls.Add(1) == 1 {
		return "", errors.New("connection reset by peer")
	}
	return "ok", nil
}

func TestDispatchResolvedToolRetriesReadOnlyTransientOnce(t *testing.T) {
	target := &transientOnceTool{}
	a := &Agent{}
	plan := &toolCallPlan{runTool: target, runArgs: json.RawMessage(`{}`), readOnly: true}
	result, _, _, err := a.dispatchResolvedTool(context.Background(), plan)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if result != "ok" || target.calls.Load() != 2 {
		t.Fatalf("result=%q calls=%d", result, target.calls.Load())
	}
}

func TestDispatchResolvedToolDoesNotRetryWriterTransient(t *testing.T) {
	target := &transientOnceTool{}
	a := &Agent{}
	plan := &toolCallPlan{
		runTool: target, runArgs: json.RawMessage(`{}`), readOnly: true,
		effects: evidence.ToolEffects{StateMutation: true},
	}
	_, _, _, err := a.dispatchResolvedTool(context.Background(), plan)
	if err == nil || target.calls.Load() != 1 {
		t.Fatalf("writer retry: err=%v calls=%d", err, target.calls.Load())
	}
}

var _ tool.Tool = (*transientOnceTool)(nil)
