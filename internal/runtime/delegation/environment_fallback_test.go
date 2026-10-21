package delegation

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/contract/event"
	"reasonix/internal/contract/provider"
	"reasonix/internal/contract/tool"
	"reasonix/internal/tools/builtin"
	"reasonix/internal/tools/jobs"
)

func TestAutomaticEnvironmentFallbackPublishesSharedEvidence(t *testing.T) {
	root := t.TempDir()
	registry := tool.NewRegistry()
	for _, writer := range builtin.ConfineWriters([]string{root}, builtin.SessionDataGuard{}, builtin.ManagedConfigPaths{}) {
		registry.Add(writer)
	}
	args, _ := json.Marshal(map[string]string{"path": filepath.Join(root, "result.txt"), "content": "shared"})
	p := &mockProvider{name: "fallback", streams: [][]provider.Chunk{
		{{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: "write", Name: "write_file", Arguments: string(args)}}, {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	task := NewTaskToolWithOptions(TaskToolOptions{Provider: p, ParentRegistry: registry, MaxSteps: 20, SysPrompt: "sys", CheckTargetAccess: allowTestFileAccess}).
		WithTranscripts(NewSubagentStore(t.TempDir()), root, "model", "high").WithOptimisticClaims(true)
	preparations := 0
	task.WithEnvironmentFactory("auto", func(context.Context, ProfileExecSpec, string) (*ExecutionEnvironment, error) {
		preparations++
		return nil, errors.New("environment unavailable")
	})
	manager := jobs.NewManager(event.Discard)
	defer manager.Close()
	ctx := jobs.WithSession(jobs.WithManager(testTaskContext(), manager), "parent-session")
	out, err := task.Execute(ctx, json.RawMessage(`{"prompt":"write result","run_in_background":true}`))
	if err != nil {
		t.Fatal(err)
	}
	jobID := extractJobID(out)
	results, _ := manager.WaitForSession(ctx, "parent-session", []string{jobID}, jobs.WaitOptions{Timeout: 5 * time.Second})
	if len(results) != 1 || results[0].Status != jobs.Done {
		t.Fatalf("fallback result: %+v", results)
	}
	if preparations != 1 {
		t.Fatalf("preparations: %d", preparations)
	}
	if summary := manager.LeaseEvidenceForSession("parent-session", jobID); !summary.HasMutation() {
		t.Fatalf("shared fallback lost evidence: %+v", summary)
	}
}
