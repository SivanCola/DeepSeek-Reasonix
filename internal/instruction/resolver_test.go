package instruction

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveKeepsMostSpecificSourceForExactDuplicate(t *testing.T) {
	root := t.TempDir()
	user := t.TempDir()
	mustWriteInstruction(t, filepath.Join(user, "AGENTS.md"), "Always run tests.")
	mustWriteInstruction(t, filepath.Join(root, "AGENTS.md"), "Always run tests.")

	got := Resolve(ResolveOptions{WorkspaceRoot: root, TargetDir: root, UserDir: user})
	if len(got.Documents) != 1 {
		t.Fatalf("documents = %+v, want one exact instruction body", got.Documents)
	}
	if got.Documents[0].Scope != ScopeProject || got.Documents[0].Path != filepath.Join(root, "AGENTS.md") {
		t.Fatalf("duplicate winner = %+v, want project source", got.Documents[0])
	}
}

func TestResolveDuplicateReplacementPreservesPrecedenceOrder(t *testing.T) {
	root := t.TempDir()
	user := t.TempDir()
	mustWriteInstruction(t, filepath.Join(user, "REASONIX.md"), "duplicate")
	mustWriteInstruction(t, filepath.Join(user, "AGENTS.md"), "unique global")
	mustWriteInstruction(t, filepath.Join(root, "AGENTS.md"), "duplicate")
	mustWriteInstruction(t, filepath.Join(root, "CLAUDE.md"), "unique project")

	got := Resolve(ResolveOptions{WorkspaceRoot: root, TargetDir: root, UserDir: user})
	if len(got.Documents) != 3 {
		t.Fatalf("documents = %+v, want three unique bodies", got.Documents)
	}
	if got.Documents[0].Body != "unique global" || got.Documents[1].Body != "duplicate" || got.Documents[2].Body != "unique project" {
		t.Fatalf("precedence order = %+v", got.Documents)
	}
	for i := range got.Documents {
		if got.Documents[i].Order != i {
			t.Fatalf("document order metadata = %+v", got.Documents)
		}
	}
}

func TestResolveKeepsDistinctConventionFilesInDeterministicOrder(t *testing.T) {
	root := t.TempDir()
	mustWriteInstruction(t, filepath.Join(root, "REASONIX.md"), "Reasonix rule")
	mustWriteInstruction(t, filepath.Join(root, "AGENTS.md"), "Portable rule")
	mustWriteInstruction(t, filepath.Join(root, "CLAUDE.md"), "Claude-compatible rule")

	got := Resolve(ResolveOptions{WorkspaceRoot: root, TargetDir: root})
	if len(got.Documents) != 3 {
		t.Fatalf("documents = %+v, want all three distinct sources", got.Documents)
	}
	for i, name := range []string{"REASONIX.md", "AGENTS.md", "CLAUDE.md"} {
		if filepath.Base(got.Documents[i].Path) != name || got.Documents[i].Order != i {
			t.Fatalf("document %d = %+v, want %s with stable order", i, got.Documents[i], name)
		}
	}
}

func TestResolveAppliesOnlyWorkspaceToTargetAncestorChain(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	target := filepath.Join(root, "services", "api")
	sibling := filepath.Join(root, "services", "web")
	for _, dir := range []string{root, target, sibling} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWriteInstruction(t, filepath.Join(parent, "AGENTS.md"), "outside workspace")
	mustWriteInstruction(t, filepath.Join(root, "AGENTS.md"), "root rule")
	mustWriteInstruction(t, filepath.Join(root, "services", "AGENTS.md"), "services rule")
	mustWriteInstruction(t, filepath.Join(target, "AGENTS.md"), "api rule")
	mustWriteInstruction(t, filepath.Join(sibling, "AGENTS.md"), "web rule")

	got := Resolve(ResolveOptions{WorkspaceRoot: root, TargetDir: target})
	joined := documentBodies(got.Documents)
	for _, want := range []string{"root rule", "services rule", "api rule"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("resolved instructions missing %q: %+v", want, got.Documents)
		}
	}
	for _, unwanted := range []string{"outside workspace", "web rule"} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("resolved instructions included %q outside target chain: %+v", unwanted, got.Documents)
		}
	}
	if got.Documents[0].Scope != ScopeProject || got.Documents[1].Scope != ScopeAncestor || got.Documents[2].Scope != ScopeAncestor {
		t.Fatalf("nested scopes = %+v", got.Documents)
	}
}

func TestResolveImportsAreProvenancedDeduplicatedAndConfined(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWriteInstruction(t, filepath.Join(root, "shared.md"), "SHARED RULE")
	mustWriteInstruction(t, filepath.Join(root, "a.md"), "A\n@shared.md")
	mustWriteInstruction(t, filepath.Join(root, "b.md"), "B\n@shared.md")
	mustWriteInstruction(t, filepath.Join(outside, "secret.md"), "SECRET")
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(root, "linked.md")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	mustWriteInstruction(t, filepath.Join(root, "AGENTS.md"), "@a.md\n@b.md\n@../secret.md\n@linked.md")

	got := Resolve(ResolveOptions{WorkspaceRoot: root, TargetDir: root})
	if len(got.Documents) != 1 {
		t.Fatalf("documents = %+v, want AGENTS.md", got.Documents)
	}
	body := got.Documents[0].Body
	if strings.Count(body, "SHARED RULE") != 1 {
		t.Fatalf("diamond import was not exactly deduplicated:\n%s", body)
	}
	for _, want := range []string{"instruction-import", "a.md", "b.md"} {
		if !strings.Contains(body, want) {
			t.Fatalf("resolved import missing provenance %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "SECRET") {
		t.Fatalf("external import escaped source directory:\n%s", body)
	}
	if len(got.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %+v, want traversal and symlink rejections", got.Diagnostics)
	}
}

func TestImportTargetClassification(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"@docs/setup.md", true}, {"@./notes.txt", true}, {"@/abs/path.md", true},
		{"@mention", false}, {"@", false}, {"@a/b and more", false}, {"plain text", false},
	} {
		if _, got := parseImportTarget(tc.line); got != tc.want {
			t.Errorf("parseImportTarget(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func documentBodies(docs []Document) string {
	var bodies []string
	for _, doc := range docs {
		bodies = append(bodies, doc.Body)
	}
	return strings.Join(bodies, "\n")
}

func mustWriteInstruction(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
