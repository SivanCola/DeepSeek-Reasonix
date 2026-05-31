package boot

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/provider"

	// Blank imports register the provider kind and built-in tools the same way
	// cmd/reasonix's main does; without them Build sees an empty provider
	// registry and a bare tool set.
	_ "reasonix/internal/provider/openai"
	_ "reasonix/internal/tool/builtin"
)

var lastCompactConfigProvider *compactConfigProvider

func init() {
	provider.Register("compact-config-test", func(provider.Config) (provider.Provider, error) {
		lastCompactConfigProvider = &compactConfigProvider{}
		return lastCompactConfigProvider, nil
	})
}

type compactConfigProvider struct {
	mainCalls int
}

func (p *compactConfigProvider) Name() string { return "compact-config-test" }

func (p *compactConfigProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 3)
	if len(req.Messages) > 0 && strings.Contains(req.Messages[0].Content, "compacting the earlier part") {
		ch <- provider.Chunk{Type: provider.ChunkText, Text: "summary from configured compaction"}
		ch <- provider.Chunk{Type: provider.ChunkDone}
		close(ch)
		return ch, nil
	}
	p.mainCalls++
	if p.mainCalls <= 2 {
		ch <- provider.Chunk{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: "call", Name: "missing_tool", Arguments: "{}"}}
		ch <- provider.Chunk{Type: provider.ChunkUsage, Usage: &provider.Usage{PromptTokens: 60, TotalTokens: 60}}
		ch <- provider.Chunk{Type: provider.ChunkDone}
		close(ch)
		return ch, nil
	}
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "done"}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

// TestBuildFoldsProjectMemoryIntoSystemPrompt is the end-to-end proof of the
// cache-first wiring: a project REASONIX.md is discovered at boot and folded
// into the session's system message (the cached prefix), and the `remember`
// tool is registered. It builds a real Controller from a throwaway project dir.
func TestBuildFoldsProjectMemoryIntoSystemPrompt(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, dir, "reasonix.toml", `
default_model = "test-model"

[agent]
system_prompt = "BASE SYSTEM PROMPT"

[[providers]]
name = "test-model"
kind = "openai"
base_url = "https://example.invalid"
model = "x"
api_key_env = "REASONIX_TEST_KEY_UNSET"
`)
	writeFile(t, dir, "REASONIX.md", "Project rule: always run go vet before committing.")

	ctrl, err := Build(context.Background(), Options{}) // RequireKey false: no network/key needed
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer ctrl.Close()

	// The system message is the cached prefix; it must contain both the base
	// prompt and the discovered memory.
	sys := systemMessage(ctrl.History())
	if !strings.Contains(sys, "BASE SYSTEM PROMPT") {
		t.Fatalf("base prompt missing from system message:\n%s", sys)
	}
	if !strings.Contains(sys, "always run go vet before committing") {
		t.Fatalf("project REASONIX.md not folded into system message:\n%s", sys)
	}
	// Base must come first so it stays a valid cache prefix when memory changes.
	if strings.Index(sys, "BASE SYSTEM PROMPT") > strings.Index(sys, "always run go vet") {
		t.Fatalf("memory should follow the base prompt, not precede it:\n%s", sys)
	}

	if mem := ctrl.Memory(); mem == nil || len(mem.Docs) == 0 {
		t.Fatal("controller memory set is empty after discovering REASONIX.md")
	}
}

// TestBuildWithoutMemoryLeavesPromptUnchanged is the inverse invariant: with no
// memory files, the system prompt is exactly the configured base — the cache
// prefix is untouched by the memory feature.
func TestBuildWithoutMemoryLeavesPromptUnchanged(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "reasonix.toml", `
default_model = "test-model"

[agent]
system_prompt = "JUST THE BASE"

[[providers]]
name = "test-model"
kind = "openai"
base_url = "https://example.invalid"
model = "x"
api_key_env = "REASONIX_TEST_KEY_UNSET"
`)

	ctrl, err := Build(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer ctrl.Close()

	sys := systemMessage(ctrl.History())
	// Note: this asserts no *project/ancestor* memory leaked in. A user-global
	// REASONIX.md in the real config dir could append; the test environment has
	// none, so the base stands alone.
	if strings.TrimSpace(sys) != "JUST THE BASE" {
		t.Fatalf("expected untouched base prompt, got:\n%s", sys)
	}
}

func TestBuildPassesAgentCompactionConfig(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "reasonix.toml", `
default_model = "test-model"

[agent]
system_prompt = "SYS"
compact_ratio = 0.5
recent_keep = 1
keep = ["errors"]

[[providers]]
name = "test-model"
kind = "compact-config-test"
base_url = "https://example.invalid"
model = "x"
context_window = 100
`)

	ctrl, err := Build(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer ctrl.Close()

	if err := ctrl.Run(context.Background(), "start"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	history := ctrl.History()
	if !historyContains(history, "summary from configured compaction") {
		t.Fatalf("compact_ratio/recent_keep were not applied; history: %+v", history)
	}
	if !historyContains(history, `error: unknown tool "missing_tool"`) {
		t.Fatalf("keep policy from config did not preserve the tool error; history: %+v", history)
	}
	if lastCompactConfigProvider == nil || lastCompactConfigProvider.mainCalls != 3 {
		t.Fatalf("provider calls = %#v, want 3 main calls", lastCompactConfigProvider)
	}
}

func systemMessage(msgs []provider.Message) string {
	for _, m := range msgs {
		if m.Role == provider.RoleSystem {
			return m.Content
		}
	}
	return ""
}

func historyContains(msgs []provider.Message, needle string) bool {
	for _, m := range msgs {
		if strings.Contains(m.Content, needle) {
			return true
		}
	}
	return false
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := writeFileRaw(dir, name, body); err != nil {
		t.Fatal(err)
	}
}
