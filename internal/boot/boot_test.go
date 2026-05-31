package boot

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/provider"
	"reasonix/internal/tool"

	// Blank imports register the provider kind and built-in tools the same way
	// cmd/reasonix's main does; without them Build sees an empty provider
	// registry and a bare tool set.
	_ "reasonix/internal/provider/openai"
	_ "reasonix/internal/tool/builtin"
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

func TestBuildCWDScopesBuiltinsAndMemory(t *testing.T) {
	base := t.TempDir()
	process := t.TempDir()
	t.Chdir(process)
	a := filepath.Join(base, "a")
	b := filepath.Join(base, "b")
	writeFile(t, a, "reasonix.toml", testConfig("a-model"))
	writeFile(t, b, "reasonix.toml", testConfig("b-model"))
	writeFile(t, a, "REASONIX.md", "workspace A rule")
	writeFile(t, b, "REASONIX.md", "workspace B rule")
	writeFile(t, a, "same.txt", "from A")
	writeFile(t, b, "same.txt", "from B")

	ctrlA, err := Build(context.Background(), Options{CWD: a})
	if err != nil {
		t.Fatalf("Build A: %v", err)
	}
	defer ctrlA.Close()
	ctrlB, err := Build(context.Background(), Options{CWD: b})
	if err != nil {
		t.Fatalf("Build B: %v", err)
	}
	defer ctrlB.Close()

	if sys := systemMessage(ctrlA.History()); !strings.Contains(sys, "workspace A rule") || strings.Contains(sys, "workspace B rule") {
		t.Fatalf("A memory not isolated:\n%s", sys)
	}
	if sys := systemMessage(ctrlB.History()); !strings.Contains(sys, "workspace B rule") || strings.Contains(sys, "workspace A rule") {
		t.Fatalf("B memory not isolated:\n%s", sys)
	}
	outA := executeReadFile(t, ctrlA, "same.txt")
	outB := executeReadFile(t, ctrlB, "same.txt")
	if !strings.Contains(outA, "from A") || !strings.Contains(outB, "from B") {
		t.Fatalf("workspace reads not isolated: A=%q B=%q", outA, outB)
	}
}

func testConfig(model string) string {
	return `
default_model = "` + model + `"

[[providers]]
name = "` + model + `"
kind = "openai"
base_url = "https://example.invalid"
model = "x"
api_key_env = "REASONIX_TEST_KEY_UNSET"
`
}

func executeReadFile(t *testing.T, ctrl interface {
	Tool(string) (tool.Tool, bool)
}, path string) string {
	t.Helper()
	read, ok := ctrl.Tool("read_file")
	if !ok {
		t.Fatal("read_file tool missing")
	}
	args, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	out, err := read.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("read_file %s: %v", path, err)
	}
	return out
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

func systemMessage(msgs []provider.Message) string {
	for _, m := range msgs {
		if m.Role == provider.RoleSystem {
			return m.Content
		}
	}
	return ""
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := writeFileRaw(dir, name, body); err != nil {
		t.Fatal(err)
	}
}
