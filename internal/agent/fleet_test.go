package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func TestFleetSchemaStableAndBounds(t *testing.T) {
	f := NewFleetTool(&TaskTool{})
	schema := string(f.Schema())
	for _, want := range []string{`"profile"`, `"write_paths"`, `"read_only"`, `"run_in_background"`} {
		if !strings.Contains(schema, want) {
			t.Fatalf("schema missing %s: %s", want, schema)
		}
	}
	// Profile names must not be enumerated in schema (cache stability).
	if strings.Contains(schema, "doc-rewriter") || strings.Contains(schema, "enum") {
		t.Fatalf("schema must not embed profile names: %s", schema)
	}
	if f.Name() != "fleet" {
		t.Fatalf("name = %q", f.Name())
	}
}

func TestFleetRejectsSingleTaskAndPathConflict(t *testing.T) {
	root := t.TempDir()
	task := newTestTaskTool(t, &mockProvider{name: "sub"}, tool.NewRegistry(), "sys", "", "", nil).
		WithTranscripts(mustSubagentStore(t), root, "base", "high").
		WithScheduler(NewSubagentScheduler(6, 3))
	f := NewFleetTool(task)

	_, err := f.Execute(context.Background(), json.RawMessage(`{"tasks":[{"prompt":"only one"}]}`))
	if err == nil || !strings.Contains(err.Error(), "between") {
		t.Fatalf("single task error = %v", err)
	}

	args, _ := json.Marshal(map[string]any{
		"tasks": []map[string]any{
			{"prompt": "a", "write_paths": []string{"same.md"}},
			{"prompt": "b", "write_paths": []string{"same.md"}},
		},
	})
	_, err = f.Execute(withCallContext(context.Background(), "fleet-call", event.Discard, nil, false), args)
	if err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("path conflict error = %v", err)
	}
}

func TestFleetParallelDisjointWriters(t *testing.T) {
	root := t.TempDir()
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32
	prov := &fleetBarrierProvider{
		onPrompt: func() {
			cur := concurrent.Add(1)
			for {
				old := maxConcurrent.Load()
				if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(30 * time.Millisecond)
			concurrent.Add(-1)
		},
	}
	reg := tool.NewRegistry()
	// No writer tools needed — provider finishes without tools.
	task := NewTaskTool(prov, nil, reg, 20, 0, 0, 0, 0, 0, 0, 0.0, "", "sys", nil, 0, "", "", nil).
		WithTranscripts(mustSubagentStore(t), root, "base", "high").
		WithScheduler(NewSubagentScheduler(10, 10))
	f := NewFleetTool(task)

	tasks := make([]map[string]any, 0, 4)
	for i := 0; i < 4; i++ {
		path := filepath.Join("docs", "f"+string(rune('0'+i))+".md")
		tasks = append(tasks, map[string]any{
			"prompt":      "handle " + path,
			"write_paths": []string{path},
			"description": path,
		})
	}
	args, _ := json.Marshal(map[string]any{"tasks": tasks})
	ctx := withCallContext(context.Background(), "fleet-call", event.Discard, nil, false)
	out, err := f.Execute(ctx, args)
	if err != nil {
		t.Fatalf("fleet: %v", err)
	}
	if !strings.Contains(out, "Completed fleet of 4") {
		t.Fatalf("output = %s", out)
	}
	if maxConcurrent.Load() < 2 {
		t.Fatalf("expected concurrent starts, max=%d", maxConcurrent.Load())
	}
}

type fleetBarrierProvider struct {
	onPrompt func()
}

func (p *fleetBarrierProvider) Name() string { return "fleet-barrier" }

func (p *fleetBarrierProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	if p.onPrompt != nil {
		p.onPrompt()
	}
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "done"}
	close(ch)
	return ch, nil
}

func mustSubagentStore(t *testing.T) *SubagentStore {
	t.Helper()
	return NewSubagentStore(t.TempDir())
}
