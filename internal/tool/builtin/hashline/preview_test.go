package hashline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/diff"
	"reasonix/internal/tool/builtin"
)

func TestHashlineEditPreviewDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.go")
	content := "line one\nline two\nline three\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	anchors := GenerateAnchors(SplitLines(content))
	ed := NewEdit(dir, []string{dir}, builtin.SessionDataGuard{}, builtin.ManagedConfigPaths{}, nil)
	args, err := json.Marshal(map[string]any{
		"path": path,
		"edits": map[string]any{
			"op":      "replace",
			"anchor":  anchors[0].Render(),
			"content": "LINE ONE",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := ed.Preview(args)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Kind != diff.Modify {
		t.Fatalf("kind = %v", ch.Kind)
	}
	if ch.Path != path {
		t.Fatalf("path = %s", ch.Path)
	}
	if ch.NewText == "" || ch.NewText == content {
		t.Fatalf("expected modified NewText, got %q", ch.NewText)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != content {
		t.Fatal("Preview must not write the file")
	}
}

func TestHashlineEditPreviewRejectsOutsideRoot(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	path := filepath.Join(outside, "secret.go")
	if err := os.WriteFile(path, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ed := NewEdit(ws, []string{ws}, builtin.SessionDataGuard{}, builtin.ManagedConfigPaths{}, nil)
	args, err := json.Marshal(map[string]any{
		"path": path,
		"edits": map[string]any{
			"op":      "write",
			"content": "x",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ed.Preview(args); err == nil {
		t.Fatal("preview outside write root must fail")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "secret\n" {
		t.Fatalf("outside file changed by Preview: %q", raw)
	}
}
