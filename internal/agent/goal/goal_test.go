package goal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSave(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".reasonix", "goal")

	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s != nil {
		t.Fatal("expected nil for non-existent state")
	}

	s = &State{Goal: "add unit tests", Status: StatusActive, Attempts: 1}
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}

	s2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Goal != s.Goal || s2.Status != s.Status || s2.Attempts != 1 {
		t.Errorf("loaded state mismatch: %+v", s2)
	}
}

func TestDelete(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".reasonix", "goal")
	s := &State{Goal: "test"}
	s.Save(dir)

	if err := Delete(dir); err != nil {
		t.Fatal(err)
	}
	s2, _ := Load(dir)
	if s2 != nil {
		t.Error("expected nil after delete")
	}
}

func TestDetectTestCommand(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example\n"), 0o644)
	if cmd := DetectTestCommand(dir); cmd != "go test ./..." {
		t.Errorf("expected go test, got %q", cmd)
	}
}

func TestPrompt(t *testing.T) {
	s := &State{Goal: "write tests", Status: StatusActive, Attempts: 1}
	p := s.Prompt()
	if p == "" {
		t.Error("empty prompt")
	}
}
