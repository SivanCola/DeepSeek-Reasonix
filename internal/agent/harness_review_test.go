package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/sandbox"
	"reasonix/internal/tool"
	"reasonix/internal/tool/builtin"
)

func TestRebuiltAgentKeepsLiveFileObservationsAndRealShellWorks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	for _, target := range (builtin.Workspace{Dir: dir, Bash: sandbox.Spec{Mode: "off"}}).Tools("read_file", "edit_file", "bash") {
		reg.Add(target)
	}
	a := New(nil, reg, NewSession(""), Options{}, event.Discard)
	run := func(a *Agent, calls []provider.ToolCall) {
		t.Helper()
		a.Session().Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: calls})
		batch := a.executeBatch(context.Background(), &a.turn, calls)
		if batch.err != nil {
			t.Fatal(batch.err)
		}
		for _, out := range batch.outcomes {
			if out.errMsg != "" || out.blocked {
				t.Fatalf("tool failed: %+v", out)
			}
		}
	}
	run(a, []provider.ToolCall{{ID: "read", Name: "read_file", Arguments: `{"path":"file","limit":1}`}})
	b := New(nil, reg, NewSession(""), Options{}, event.Discard)
	b.InheritFileObservationsFrom(a)
	run(b, []provider.ToolCall{
		{ID: "edit", Name: "edit_file", Arguments: `{"path":"file","old_string":"old","new_string":"new"}`},
		{ID: "shell", Name: "bash", Arguments: `{"command":"git --version"}`},
		{ID: "edit-again", Name: "edit_file", Arguments: `{"path":"file","old_string":"new","new_string":"final"}`},
	})
}
