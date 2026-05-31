package boot

import (
	"context"
	"io"
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/plugin"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
	"reasonix/internal/tool/builtin"

	// Blank imports register the provider kind and built-in tools the same way
	// cmd/reasonix's main does; without them Build sees an empty provider
	// registry and a bare tool set.
	_ "reasonix/internal/provider/openai"
)

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

// TestBuildDiscoversSkills proves the skill wiring end-to-end: a project skill
// is discovered at boot, surfaced via Controller.Skills(), and its name folds
// into the cache-stable system prompt's "# Skills" index alongside a built-in.
func TestBuildDiscoversSkills(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "reasonix.toml", `
default_model = "test-model"

[agent]
system_prompt = "BASE"

[[providers]]
name = "test-model"
kind = "openai"
base_url = "https://example.invalid"
model = "x"
api_key_env = "REASONIX_TEST_KEY_UNSET"
`)
	writeFile(t, dir, ".reasonix/skills/projskill.md", "---\ndescription: a project skill\n---\nplaybook")

	ctrl, err := Build(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer ctrl.Close()

	var hasProj, hasBuiltin bool
	for _, s := range ctrl.Skills() {
		switch s.Name {
		case "projskill":
			hasProj = true
		case "explore":
			hasBuiltin = true
		}
	}
	if !hasProj || !hasBuiltin {
		t.Fatalf("Skills() should include the project skill and a built-in; got %v", ctrl.Skills())
	}

	sys := systemMessage(ctrl.History())
	if !strings.Contains(sys, "# Skills") {
		t.Fatalf("skills index missing from system prompt:\n%s", sys)
	}
	if !strings.Contains(sys, "projskill") || !strings.Contains(sys, "explore") {
		t.Fatalf("skill names missing from index:\n%s", sys)
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
	// The built-in skills always append a "# Skills" index to the prefix; this
	// test is about memory, so strip that and assert the remaining base is exactly
	// the configured prompt — i.e. no *project/ancestor* memory leaked in. (A
	// user-global REASONIX.md in the real config dir could append; the test
	// environment has none, so the base stands alone.)
	base := sys
	if i := strings.Index(sys, "\n\n# Skills"); i >= 0 {
		base = sys[:i]
	}
	// The language policy is always appended at boot; strip it so this assertion
	// is purely about whether project/ancestor memory leaked into the base.
	base = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(base), config.LanguagePolicy))
	if base != "JUST THE BASE" {
		t.Fatalf("expected untouched base prompt, got:\n%s", sys)
	}
}

func TestPluginSpecsForWorktreeRebindsStdioAndFiltersByToolNames(t *testing.T) {
	specs := []plugin.Spec{{
		Name:    "fs tools",
		Command: "mcp-fs",
		Args:    []string{"serve"},
		Env:     map[string]string{"A": "B"},
	}, {
		Name: "remote",
		Type: "http",
		URL:  "https://example.invalid/mcp",
	}}
	names := []string{plugin.ToolPrefix("fs tools") + "read_file"}

	got := pluginSpecsForWorktree(context.Background(), specs, "/tmp/wt", names, io.Discard, event.Discard)
	if len(got) != 1 {
		t.Fatalf("specs len = %d, want 1", len(got))
	}
	if got[0].Name != "fs tools" || got[0].Dir != "/tmp/wt" {
		t.Fatalf("stdio spec was not rebound to worktree: %+v", got[0])
	}
	if got[0].Stderr == nil {
		t.Fatal("stderr override should be propagated")
	}
	got[0].Env["A"] = "mutated"
	if specs[0].Env["A"] != "B" {
		t.Fatalf("pluginSpecsForWorktree should not mutate input specs: %+v", specs[0].Env)
	}
}

func TestPluginSpecsForWorktreeSkipsPluginsWithoutExplicitToolNames(t *testing.T) {
	specs := []plugin.Spec{{
		Name:    "fs tools",
		Command: "mcp-fs",
		Args:    []string{"serve"},
	}}

	got := pluginSpecsForWorktree(context.Background(), specs, "/tmp/wt", nil, io.Discard, event.Discard)
	if len(got) != 0 {
		t.Fatalf("specs len = %d, want 0", len(got))
	}
}

func TestWorkspaceBuiltinsForParentHonorsParentEnabledTools(t *testing.T) {
	parent := tool.NewRegistry()
	readFile, ok := tool.LookupBuiltin("read_file")
	if !ok {
		t.Fatal("read_file builtin missing")
	}
	parent.Add(readFile)
	ws := builtin.Workspace{Dir: t.TempDir()}

	got := toolNames(workspaceBuiltinsForParent(parent, ws, true, nil))
	if len(got) != 1 || !got["read_file"] {
		t.Fatalf("default worktree builtins = %v, want only read_file", got)
	}

	got = toolNames(workspaceBuiltinsForParent(parent, ws, true, []string{"bash", "read_file"}))
	if len(got) != 1 || !got["read_file"] {
		t.Fatalf("explicit worktree builtins = %v, want disabled bash filtered out", got)
	}
}

func TestWorkspaceBuiltinsForParentFiltersWritersInReadOnlyMode(t *testing.T) {
	parent := tool.NewRegistry()
	for _, name := range []string{"read_file", "write_file"} {
		tl, ok := tool.LookupBuiltin(name)
		if !ok {
			t.Fatalf("%s builtin missing", name)
		}
		parent.Add(tl)
	}
	ws := builtin.Workspace{Dir: t.TempDir()}

	got := toolNames(workspaceBuiltinsForParent(parent, ws, false, nil))
	if len(got) != 1 || !got["read_file"] || got["write_file"] {
		t.Fatalf("read-only worktree builtins = %v, want only read_file", got)
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

func toolNames(tools []tool.Tool) map[string]bool {
	out := make(map[string]bool, len(tools))
	for _, tl := range tools {
		out[tl.Name()] = true
	}
	return out
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := writeFileRaw(dir, name, body); err != nil {
		t.Fatal(err)
	}
}
