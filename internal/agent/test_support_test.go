package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/fileops"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// readProbe is a small read-only tool used by budget and lifecycle tests.
type readProbe struct{}

func (readProbe) Name() string        { return "read_file" }
func (readProbe) Description() string { return "read a file" }
func (readProbe) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)
}
func (readProbe) ReadOnly() bool { return true }
func (readProbe) Execute(context.Context, json.RawMessage) (string, error) {
	return "package main\n\nfunc main() {}\n", nil
}

type recoveryArgumentTool struct {
	name   string
	schema json.RawMessage
	mu     sync.Mutex
	inputs []string
}

func (t *recoveryArgumentTool) Name() string            { return t.name }
func (t *recoveryArgumentTool) Description() string     { return "argument fixture" }
func (t *recoveryArgumentTool) Schema() json.RawMessage { return t.schema }
func (t *recoveryArgumentTool) ReadOnly() bool          { return true }
func (t *recoveryArgumentTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inputs = append(t.inputs, string(args))
	return "executed", nil
}

type readinessAuditSink struct{ events []evidence.ReadinessAudit }

func (*readinessAuditSink) Emit(event.Event) {}
func (s *readinessAuditSink) RecordReadinessAudit(a evidence.ReadinessAudit) {
	s.events = append(s.events, a)
}

type okTool struct{ name string }

func (t okTool) Name() string                                           { return t.name }
func (okTool) Description() string                                      { return "ok" }
func (okTool) ReadOnly() bool                                           { return true }
func (okTool) Schema() json.RawMessage                                  { return json.RawMessage(`{"type":"object"}`) }
func (okTool) Execute(context.Context, json.RawMessage) (string, error) { return "ok", nil }

type failTool struct{ name string }

func (t failTool) Name() string          { return t.name }
func (failTool) Description() string     { return "fail" }
func (failTool) ReadOnly() bool          { return true }
func (failTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (failTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "", errors.New("boom")
}

func stripReceiptCitation(result string) string { return result }

type fakeReadFileTool struct{}

func (fakeReadFileTool) Name() string            { return "read_file" }
func (fakeReadFileTool) Description() string     { return "fake read" }
func (fakeReadFileTool) ReadOnly() bool          { return true }
func (fakeReadFileTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (fakeReadFileTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "contents", nil
}

type fakeWriterTool struct{}

func (fakeWriterTool) Name() string            { return "fake_write" }
func (fakeWriterTool) Description() string     { return "fake write" }
func (fakeWriterTool) ReadOnly() bool          { return false }
func (fakeWriterTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (fakeWriterTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "wrote", nil
}

func executeBatchOutputs(a *Agent, ctx context.Context, calls []provider.ToolCall) []string {
	return a.executeBatch(ctx, &a.turn, calls).results
}

type scriptedProvider struct {
	name     string
	turns    [][]provider.Chunk
	call     int
	requests []provider.Request
}

func (s *scriptedProvider) Name() string { return s.name }

func (s *scriptedProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	s.requests = append(s.requests, req)
	i := s.call
	if i >= len(s.turns) {
		i = len(s.turns) - 1
	}
	s.call++
	ch := make(chan provider.Chunk, len(s.turns[i]))
	for _, chunk := range s.turns[i] {
		ch <- chunk
	}
	close(ch)
	return ch, nil
}

func toolCallChunk(id, name, args string) provider.Chunk {
	return provider.Chunk{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: id, Name: name, Arguments: args}}
}

func toolResult(s *Session, name string) string {
	for _, message := range s.Messages {
		if message.Role == provider.RoleTool && message.Name == name {
			return message.Content
		}
	}
	return ""
}

func lastToolResult(s *Session, name string) string {
	var result string
	for _, message := range s.Messages {
		if message.Role == provider.RoleTool && message.Name == name {
			result = message.Content
		}
	}
	return result
}

func toolResultByID(s *Session, id string) string {
	for _, message := range s.Messages {
		if message.Role == provider.RoleTool && message.ToolCallID == id {
			return message.Content
		}
	}
	return ""
}

func toolResults(s *Session, name string) []string {
	var results []string
	for _, message := range s.Messages {
		if message.Role == provider.RoleTool && message.Name == name {
			results = append(results, message.Content)
		}
	}
	return results
}

func sessionHasUserMessageContaining(s *Session, needle string) bool {
	for _, message := range s.Messages {
		if message.Role == provider.RoleUser && strings.Contains(message.Content, needle) {
			return true
		}
	}
	return false
}

func readinessLedger(receipts ...evidence.Receipt) *evidence.Ledger {
	ledger := evidence.NewLedger()
	for _, receipt := range receipts {
		ledger.Record(receipt)
	}
	return ledger
}

func mustBuiltinTool(t *testing.T, name string) tool.Tool {
	t.Helper()
	value, ok := tool.LookupBuiltin(name)
	if !ok {
		t.Fatalf("missing builtin %q", name)
	}
	return value
}

func incompleteReadBuiltin(t *testing.T) tool.Tool { return mustBuiltinTool(t, "read_file") }

func seedPriorRead(t *testing.T, a *Agent, path string) {
	t.Helper()
	reader := incompleteReadBuiltin(t).(tool.ReadExecutor)
	args, _ := json.Marshal(map[string]any{"path": path, "limit": 1})
	ctx := fileops.WithStore(context.Background(), a.fileObservations)
	if _, _, err := reader.ExecuteRead(ctx, args); err != nil {
		t.Fatal(err)
	}
}

type stubBash struct{}

func (stubBash) Name() string                                             { return "bash" }
func (stubBash) Description() string                                      { return "stub bash" }
func (stubBash) ReadOnly() bool                                           { return false }
func (stubBash) Schema() json.RawMessage                                  { return json.RawMessage(`{"type":"object"}`) }
func (stubBash) Execute(context.Context, json.RawMessage) (string, error) { return "ok", nil }

type stubWrite struct{}

func (stubWrite) Name() string                                             { return "write_file" }
func (stubWrite) Description() string                                      { return "stub write" }
func (stubWrite) ReadOnly() bool                                           { return false }
func (stubWrite) Schema() json.RawMessage                                  { return json.RawMessage(`{"type":"object"}`) }
func (stubWrite) Execute(context.Context, json.RawMessage) (string, error) { return "wrote", nil }

func evidenceRegistry() *tool.Registry {
	reg := tool.NewRegistry()
	if todo, ok := tool.LookupBuiltin("todo_write"); ok {
		reg.Add(todo)
	}
	reg.Add(stubBash{})
	reg.Add(stubWrite{})
	return reg
}
