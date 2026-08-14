package checkpoint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestV3PersistAndReload(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(t.TempDir(), "ckpt")
	s := New(dir, root)
	s.Begin(1, "edit", 0)
	target := filepath.Join(root, "a.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.CaptureBefore(target, CaptureBeforeOpts{})

	meta := filepath.Join(dir, "turns", "1", "meta.json")
	if _, err := os.Stat(meta); err != nil {
		t.Fatalf("v3 meta missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "turn-1.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy turn-1.json should not be written: %v", err)
	}
	before := filepath.Join(dir, "turns", "1", "files", "0000.before")
	raw, err := os.ReadFile(before)
	if err != nil {
		t.Fatalf("before payload: %v", err)
	}
	if string(raw) != "hello" {
		t.Fatalf("before payload = %q", raw)
	}

	reloaded := New(dir, root)
	if len(reloaded.done) != 1 || reloaded.done[0].Turn != 1 || reloaded.done[0].SchemaVersion != SchemaV3 {
		t.Fatalf("reloaded = %+v", reloaded.done)
	}
	got := reloaded.done[0].Files
	if len(got) != 1 || got[0].Content == nil || *got[0].Content != "hello" {
		t.Fatalf("reloaded files = %+v", got)
	}
}

func TestV3LoadKeepsLegacyTurnJSON(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ckpt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"schemaVersion":2,"turn":0,"prompt":"old","files":[{"path":"a.txt","content":"v2"}]}`)
	if err := os.WriteFile(filepath.Join(dir, "turn-0.json"), legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(dir, t.TempDir())
	s.Begin(1, "new", 1)
	s.Begin(2, "flush", 2)
	reloaded := New(dir, t.TempDir())
	if len(reloaded.done) < 1 {
		t.Fatal("expected reloaded checkpoints")
	}
	var sawLegacy, sawV3 bool
	for _, c := range reloaded.done {
		if c.Turn == 0 && c.SchemaVersion == SchemaV2 {
			sawLegacy = true
		}
		if c.Turn == 1 && c.SchemaVersion == SchemaV3 {
			sawV3 = true
		}
	}
	if !sawLegacy || !sawV3 {
		t.Fatalf("legacy=%v v3=%v done=%+v", sawLegacy, sawV3, reloaded.done)
	}
}
