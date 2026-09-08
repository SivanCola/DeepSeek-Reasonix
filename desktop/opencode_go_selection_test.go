package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

func TestOpenCodeGoExplicitReselectionSurvivesRestart(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "GO_SELECTION_KEY", "fixture-key")
	const ref = "go/deepseek-v4-flash"
	cfg := config.Default()
	cfg.ConfigVersion = 9
	cfg.DefaultModel, cfg.Agent.PlannerModel = ref, ref
	cfg.Desktop.ProviderAccess = []string{"go"}
	cfg.Providers = []config.ProviderEntry{{Name: "go", Kind: "anthropic", BaseURL: "https://opencode.ai/zen/go", Model: "deepseek-v4-flash", APIKeyEnv: "GO_SELECTION_KEY"}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}
	if _, err := config.ApplyUserConfigUpgradesOnStartup(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "historical.jsonl")
	s := agent.NewSession("fixture system")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "preserve this history"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := agent.SetBranchModelPreserveUpdated(path, ref); err != nil {
		t.Fatal(err)
	}
	old, err := boot.Build(context.Background(), boot.Options{Model: ref, Sink: event.Discard, SessionDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	app.ctx, app.readyHook = context.Background(), func() {}
	tab := reloadRuntimeTab(t, app, dir, old)
	tab.model, tab.SessionPath = ref, path
	old.Resume(s, path)
	cfg = config.LoadForEdit(config.UserConfigPath())
	cfg.Providers[0].NoProxy = true
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.LoadUserConfigReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(agent.BranchMetaPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.resolveStartupModel(cfg, ref, path, tab.ID); err == nil || !strings.Contains(err.Error(), "MIGRATED_MODEL_UNAVAILABLE") {
		t.Fatalf("legacy restore did not reject edited connection: %v", err)
	}
	if err := app.snapshotTabWorkspaceForRebuild(tab, old, tab.WorkspaceRoot); err == nil {
		t.Fatal("workspace rebuild skipped history validation")
	}
	if err := app.ReloadRuntime(tab.ID); err == nil {
		t.Fatal("implicit reload adopted changed connection")
	}
	after, _ := os.ReadFile(agent.BranchMetaPath(path))
	if string(before) != string(after) || tab.Ctrl != old || len(old.History()) != len(s.Snapshot()) {
		t.Fatal("failed restore changed the old runtime or sidecar")
	}
	// Simulate the next startup having failed before publishing a controller.
	old.Close()
	tab.Ctrl = nil
	tab.Ready, tab.StartupErr = false, "MIGRATED_MODEL_UNAVAILABLE"
	if err := app.SetModelForTab(tab.ID, ref); err != nil {
		t.Fatalf("same-name explicit reselection: %v", err)
	}
	if tab.Ctrl == nil || !tab.Ready || tab.StartupErr != "" {
		t.Fatal("same-name reselection did not restore a ready runtime")
	}
	model, identity, ok := agent.LoadSessionModelSelection(path)
	if !ok || model != ref || identity != cfg.ModelSelectionIdentity(ref) || identity == "" {
		t.Fatal("explicit selection identity was not persisted")
	}
	if got, err := app.resolveStartupModel(cfg, ref, path, tab.ID); err != nil || got != ref {
		t.Fatalf("restart rejected acknowledged selection: %q, %v", got, err)
	}
	found := false
	for _, msg := range tab.Ctrl.History() {
		found = found || msg.Content == "preserve this history"
	}
	if !found {
		t.Fatal("reselection lost the historical transcript")
	}
	// Choosing a default model is also an explicit selection; it must not be
	// mistaken for an implicit settings reload of the old identity.
	cfg.Providers[0].Headers = map[string]string{"X-User": "updated"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}
	if err := app.SetDefaultModel(ref); err != nil {
		t.Fatalf("explicit default selection was treated as historical: %v", err)
	}
	latest, err := config.LoadUserConfigReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	if _, identity, ok := agent.LoadSessionModelSelection(path); !ok || identity != latest.ModelSelectionIdentity(ref) {
		t.Fatal("default selection did not acknowledge the current connection")
	}
}
