package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

func TestOpenCodeGoUpgradeEffortSwitchPreservesPlannerAndHistory(t *testing.T) {
	for _, model := range []string{"deepseek-v4-flash", "deepseek-v4-pro", "deepseek-v4-flash-vision-exp"} {
		t.Run(model, func(t *testing.T) {
			isolateDesktopUserDirs(t)
			setDesktopTestCredential(t, "GO_TEST_KEY", "fixture-key")
			cfg := config.Default()
			cfg.ConfigVersion = 9
			ref := "go/" + model
			cfg.DefaultModel, cfg.Agent.PlannerModel = ref, ref
			cfg.Desktop.ProviderAccess = []string{"go"}
			cfg.Providers = []config.ProviderEntry{{Name: "go", Kind: "anthropic", BaseURL: "https://opencode.ai/zen/go", RequestURL: "https://opencode.ai/zen/go/v1/messages", Model: model, APIKeyEnv: "GO_TEST_KEY", Thinking: "enabled", Effort: "max", MaxOutputTokens: 128}}
			if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
				t.Fatal(err)
			}
			auto := ""
			ctrl, err := boot.Build(context.Background(), boot.Options{Model: ref, EffortOverride: &auto, Sink: event.Discard, SessionDir: config.SessionDir()})
			if err != nil {
				t.Fatalf("legacy new session: %v", err)
			}
			app := NewApp()
			app.ctx, app.readyHook = context.Background(), func() {}
			tab := reloadRuntimeTab(t, app, config.SessionDir(), ctrl)
			tab.model, tab.effort = ref, &auto
			path := filepath.Join(config.SessionDir(), "opencode-history.jsonl")
			ctrl.AdoptHistory([]provider.Message{
				{Role: provider.RoleSystem, Content: "fixture system", ID: "system-1"},
				{Role: provider.RoleUser, Content: "fixture task", ID: "user-1"},
				{Role: provider.RoleAssistant, ReasoningContent: "fixture provider reasoning", ToolCalls: []provider.ToolCall{{ID: "tool-1", Name: "read_file", Arguments: `{"path":"README.md"}`}}, ID: "assistant-1"},
				{Role: provider.RoleTool, ToolCallID: "tool-1", Content: "fixture result", ID: "result-1"},
				{Role: provider.RoleAssistant, Content: "fixture done", ID: "assistant-2"},
			}, path)
			tab.SessionPath = path
			if got := app.EffortForTab(tab.ID); got.Current != "auto" {
				t.Fatalf("initial effort %+v", got)
			}
			if err := app.SetEffortForTab(tab.ID, "disabled"); err != nil {
				t.Fatalf("auto to disabled with planner max: %v", err)
			}
			if tab.effort == nil || *tab.effort != "disabled" || tab.Ctrl == ctrl {
				t.Fatal("effort switch did not commit")
			}
			if tab.Ctrl.SessionPath() != path {
				t.Fatal("switch changed history path")
			}
			loaded, err := config.LoadUserConfigReadOnly()
			if err != nil {
				t.Fatal(err)
			}
			planner, ok := loaded.ResolveModel(loaded.Agent.PlannerModel)
			if !ok || planner.Effort != "max" || planner.Kind != "openai" || loaded.ConfigVersion != 10 {
				t.Fatalf("planner or migration changed unexpectedly: %+v", planner)
			}
			loaded.Agent.PlannerModel = "invalid/" + model
			loaded.Providers = append(loaded.Providers, config.ProviderEntry{Name: "invalid", Kind: "anthropic", BaseURL: "https://custom.example", Model: model, Thinking: "enabled", Effort: "max"})
			if err := loaded.SaveTo(config.UserConfigPath()); err != nil {
				t.Fatal(err)
			}
			beforeCtrl, beforeHistory := tab.Ctrl, tab.Ctrl.History()
			err = app.SetEffortForTab(tab.ID, "high")
			var role *boot.RoleReasoningError
			var unsupported *provider.UnsupportedReasoningEffort
			if !errors.As(err, &role) || !errors.As(err, &unsupported) || role.Role != "planner" {
				t.Fatalf("invalid planner: %v", err)
			}
			if tab.Ctrl != beforeCtrl || tab.Ctrl.SessionPath() != path || *tab.effort != "disabled" || !reflect.DeepEqual(beforeHistory, tab.Ctrl.History()) {
				t.Fatal("failed switch changed the live runtime/history/effort")
			}
			loaded.Agent.PlannerModel = ref
			if err := loaded.SaveTo(config.UserConfigPath()); err != nil {
				t.Fatal(err)
			}
			if err := app.SetEffortForTab(tab.ID, "high"); err != nil {
				t.Fatalf("old runtime was not usable for retry: %v", err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("history was not retained", err)
			}
		})
	}
}

func TestOpenCodeGoStartupNoticeSurvivesEarlyMigrationAndDeliversOnce(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	cfg.ConfigVersion = 9
	cfg.Providers = []config.ProviderEntry{{Name: "go", Kind: "anthropic", BaseURL: "https://opencode.ai/zen/go", Model: "deepseek-v4-pro", Thinking: "enabled", Effort: "max"}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}
	changed, err := config.ApplyUserConfigUpgradesOnStartup(config.UserConfigPath())
	if err != nil || !changed {
		t.Fatalf("upgrade: %v", err)
	}
	loaded, err := config.LoadUserConfigReadOnly()
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	app.queueStartupConfigNotice(loaded, changed, nil)
	active := &WorkspaceTab{ID: "active", sink: &tabEventSink{}}
	app.activeTabID = active.ID
	if notice, _ := app.takeStartupConfigNotice(active); notice != nil {
		t.Fatal("notice emitted before the renderer subscribed")
	}
	app.startupReady.Store(true)
	if notice, _ := app.takeStartupConfigNotice(&WorkspaceTab{ID: "background", sink: &tabEventSink{}}); notice != nil {
		t.Fatal("background tab consumed notice")
	}
	if notice, sink := app.takeStartupConfigNotice(active); notice == nil || notice.Detail == "" || sink != active.sink {
		t.Fatal("upgrade notice was lost")
	}
	if notice, _ := app.takeStartupConfigNotice(active); notice != nil {
		t.Fatal("duplicate notice")
	}
	app.queueStartupConfigNotice(nil, false, errors.New("commit migration: permission denied"))
	if notice, _ := app.takeStartupConfigNotice(active); notice == nil || notice.Level != event.LevelWarn || notice.Detail != "commit migration: permission denied" {
		t.Fatal("failure reason was lost")
	}
}

func TestReloadRuntimeRetriesFailedNewSession(t *testing.T) {
	dir := reloadRuntimeFixture(t)
	app := NewApp()
	app.ctx, app.readyHook = context.Background(), func() {}
	tab := reloadRuntimeTab(t, app, dir, nil)
	tab.Ctrl = nil // Avoid a typed nil in the SessionAPI interface in this fixture.
	tab.Ready, tab.StartupErr = false, "previous provider construction failure"
	if err := app.ReloadRuntime(tab.ID); err != nil {
		t.Fatal(err)
	}
	if tab.Ctrl == nil || !tab.Ready || tab.StartupErr != "" {
		t.Fatal("retry did not reach ready state")
	}
}

// A failed startup leaves the transcript on disk only. Retrying through the
// runtime reload must resume that transcript, not bind an empty history to it.
func TestReloadRuntimeRetryResumesSavedTranscript(t *testing.T) {
	dir := reloadRuntimeFixture(t)
	path := filepath.Join(dir, "historical.jsonl")
	s := agent.NewSession("fixture system")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "preserve this history"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "kept"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	app.ctx, app.readyHook = context.Background(), func() {}
	tab := reloadRuntimeTab(t, app, dir, nil)
	tab.Ctrl = nil
	tab.SessionPath = path
	tab.Ready, tab.StartupErr = false, "previous provider construction failure"
	if err := app.ReloadRuntime(tab.ID); err != nil {
		t.Fatal(err)
	}
	if tab.Ctrl == nil || tab.Ctrl.SessionPath() != path {
		t.Fatalf("retry did not bind the saved session: %v", tab.Ctrl)
	}
	var contents []string
	for _, m := range tab.Ctrl.History() {
		contents = append(contents, m.Content)
	}
	if !strings.Contains(strings.Join(contents, "\n"), "preserve this history") {
		t.Fatalf("retry discarded the saved transcript: %q", contents)
	}
}
