package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateAndWriteRefusesConcurrentModification(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	body, err := renderTOMLForScopeErr(Default(), RenderScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stateID, err := configFileStateID(path)
	if err != nil {
		t.Fatal(err)
	}
	// Another process modifies the file between edit-read and write.
	if err := os.WriteFile(path, []byte("default_model = \"other\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := writeConfigOptions{scope: RenderScopeUser}
	err = validateAndWriteConfigResolved(path, body, 0o600, opts, stateID)
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("expected concurrent-change error, got %v", err)
	}
	// The concurrent content survives.
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "other") {
		t.Errorf("concurrent write was overwritten: %q", b)
	}
}

func TestValidateAndWriteRequiresParseableOutput(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	// A body with an invalid TOML escape must never be written.
	err := validateAndWriteConfigResolved(path, "command = \"D:\\开发\\x.exe\"\n", 0o600, writeConfigOptions{scope: RenderScopeUser}, "")
	if err == nil {
		t.Fatal("write of unparseable config succeeded")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("file was created despite validation failure: %v", statErr)
	}
}

func TestConfigFileStateIDChangesWithContent(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("a = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := configFileStateID(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("a = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := configFileStateID(path)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("state ID did not change with content")
	}
	absent, err := configFileStateID(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if absent != "absent" {
		t.Fatalf("missing file state = %q, want absent", absent)
	}
}

func TestValidateAndWriteDetectsSilentlyDroppedField(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	// A body whose decoded config does NOT match the intended config (the
	// renderer "forgot" default_model) must be rejected by the persisted
	// semantics comparison.
	want := Default()
	want.DefaultModel = "deepseek-pro"
	body, err := renderTOMLForScopeErr(want, RenderScopeUser)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a renderer dropping the field: strip default_model from the body.
	lines := strings.Split(body, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if strings.HasPrefix(line, "default_model") {
			continue
		}
		kept = append(kept, line)
	}
	opts := writeConfigOptions{scope: RenderScopeUser, want: want}
	err = validateAndWriteConfigResolved(path, strings.Join(kept, "\n"), 0o600, opts, "")
	if err == nil || !strings.Contains(err.Error(), "default_model") {
		t.Fatalf("dropped-field body accepted: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatal("dropped-field body was written")
	}
}

func TestLoadForEditSaveToRefusesStaleSnapshot(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	original := Default()
	original.DefaultModel = "deepseek-flash"
	if err := original.WriteFile(path); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadForEditReadOnlyStrict(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := cfg.SetDefaultModel("deepseek-pro"); err != nil {
		t.Fatal(err)
	}

	// Another process replaces the file after load.
	if err := os.WriteFile(path, []byte("default_model = \"hijacked\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err = cfg.SaveTo(path)
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("expected concurrent-change error on public Load→Save path, got %v", err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "hijacked") {
		t.Fatalf("stale SaveTo overwrote concurrent content: %s", body)
	}
	if strings.Contains(string(body), "deepseek-pro") {
		t.Fatalf("stale edit was persisted: %s", body)
	}
}

func TestLoadForEditSaveToRefusesConcurrentCreateOfAbsentFile(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "missing.toml")

	cfg, err := LoadForEditReadOnlyStrict(path)
	if err != nil {
		t.Fatalf("load absent: %v", err)
	}
	if !cfg.editOriginBound || cfg.editOriginState != "absent" {
		t.Fatalf("edit origin = bound:%v state:%q, want absent", cfg.editOriginBound, cfg.editOriginState)
	}
	if err := cfg.SetDefaultModel("deepseek-pro"); err != nil {
		t.Fatal(err)
	}

	// Another process creates the file before our create-only publish.
	if err := os.WriteFile(path, []byte("default_model = \"already-there\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err = cfg.SaveTo(path)
	if err == nil {
		t.Fatal("expected concurrent-create error, save succeeded")
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "already-there") {
		t.Fatalf("create-only path overwrote concurrent create: %s", body)
	}
	if strings.Contains(string(body), "deepseek-pro") {
		t.Fatalf("stale absent-origin save was persisted: %s", body)
	}
}

func TestProjectDeltaValidationDetectsDroppedCustomProviderField(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "reasonix.toml")
	if err := os.WriteFile(path, []byte("default_model = \"deepseek-flash\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Delta claims a custom provider with base_url, but the merged body drops it.
	// Built-in providers would occupy providers[0] under index comparison.
	delta := `
[[providers]]
name     = "custom-relay"
kind     = "openai"
base_url = "https://relay.example/v1"
model    = "relay-model"
`
	// Body retains only the default_model line — custom provider missing entirely.
	body := "default_model = \"deepseek-flash\"\n"
	opts := writeConfigOptions{
		scope: RenderScopeProject,
		delta: delta,
	}
	err := validateAndWriteConfigResolved(path, body, 0o644, opts, "")
	if err == nil {
		t.Fatal("dropped custom provider field was accepted")
	}
	if !strings.Contains(err.Error(), "custom-relay") && !strings.Contains(err.Error(), "base_url") && !strings.Contains(err.Error(), "providers") {
		t.Fatalf("error should mention the custom provider field, got %v", err)
	}
	// Original file preserved.
	got, _ := os.ReadFile(path)
	if string(got) != "default_model = \"deepseek-flash\"\n" {
		t.Fatalf("original project file changed: %q", got)
	}
}

func TestExtraChecksRunWithoutDelta(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "reasonix.toml")
	if err := os.WriteFile(path, []byte("[desktop]\nlegacy = \"keep\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Body claims provider_access but value differs from extraChecks.
	body := "[desktop]\nlegacy = \"keep\"\nprovider_access = [\"other\"]\n"
	opts := writeConfigOptions{
		scope:       RenderScopeProject,
		extraChecks: map[string]any{"desktop.provider_access": []string{"expected"}},
	}
	err := validateAndWriteConfigResolved(path, body, 0o644, opts, "")
	if err == nil {
		t.Fatal("extraChecks with empty delta were skipped")
	}
	if !strings.Contains(err.Error(), "provider_access") {
		t.Fatalf("expected provider_access drift, got %v", err)
	}
}
