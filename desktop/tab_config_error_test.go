package main

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/repair"
)

func TestConfigLoadErrorCarriesPathAndLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	root := t.TempDir()
	project := filepath.Join(root, "reasonix.toml")
	// Broken TOML at a known line.
	if err := os.WriteFile(project, []byte("[agent]\nreasoning_language = \"zh\"\n[broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.LoadForRoot(root)
	if err == nil {
		t.Fatal("expected load failure")
	}
	cle, ok := config.ConfigLoadErrorOf(err)
	if !ok {
		t.Fatalf("error is not a ConfigLoadError: %v", err)
	}
	if filepath.Clean(cle.Path) != filepath.Clean(project) {
		t.Errorf("path = %q, want %q", cle.Path, project)
	}
	if cle.Line < 3 {
		t.Errorf("line = %d, want the broken header region (>= 3)", cle.Line)
	}
}

func TestIsGlobalConfigFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	user := config.UserConfigPath()
	if !isGlobalConfigFile(user) {
		t.Errorf("%q should be global", user)
	}
	if isGlobalConfigFile(filepath.Join(t.TempDir(), "reasonix.toml")) {
		t.Error("project config classified as global")
	}
}

func TestApplyProjectConfigFixRequiresPreview(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	root := t.TempDir()
	project := filepath.Join(root, "reasonix.toml")
	if err := os.WriteFile(project, []byte("[[plugins]]\ncommand = \"D:\\开发\\x.exe\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Preview scan finds the escape fix.
	fixes, err := scanProjectConfigEscapes(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(fixes) != 1 {
		t.Fatalf("fixes = %d, want 1", len(fixes))
	}
	// Confirmed apply through the repair pipeline (state-bound).
	expected := map[string]string{project: repair.FileStateID(project)}
	report, err := repair.ApplyConfigEscapes(repair.ConfigEscapesOptions{Root: root, IncludeProject: true, ExpectedStates: expected})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Project.Applied {
		t.Fatalf("confirmed project apply failed: %+v", report.Project)
	}
	b, _ := os.ReadFile(project)
	if err := config.ValidateBytes(b); err != nil {
		t.Fatalf("repaired project config does not parse: %v", err)
	}
}
