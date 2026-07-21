package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/control"
)

// TestBuildTabControllerHonorsProjectAutoRecoveryKillSwitch exercises the
// desktop wiring that runs after boot.Build. A user-global "on" must never be
// re-applied over a project-level "off", either while the tab controller is
// configured or when it binds a fresh session.
func TestBuildTabControllerHonorsProjectAutoRecoveryKillSwitch(t *testing.T) {
	isolateDesktopUserDirs(t)
	userCfg := config.UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(userCfg), 0o755); err != nil {
		t.Fatalf("mkdir user config: %v", err)
	}
	if err := os.WriteFile(userCfg, []byte(`
default_model = "test-model"

[agent]
auto_recovery_checkpoint = "on"

[[providers]]
name = "test-model"
kind = "openai"
base_url = "https://example.invalid"
model = "x"
api_key_env = "REASONIX_TEST_KEY_UNSET"
`), 0o644); err != nil {
		t.Fatalf("write user config: %v", err)
	}

	root := robustTempDir(t)
	if err := os.WriteFile(filepath.Join(root, "reasonix.toml"), []byte(`
default_model = "test-model"

[agent]
auto_recovery_checkpoint = "off"

[[providers]]
name = "test-model"
kind = "openai"
base_url = "https://example.invalid"
model = "x"
api_key_env = "REASONIX_TEST_KEY_UNSET"
`), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	tab := &WorkspaceTab{
		ID:               "project-auto-guard-off",
		Scope:            "project",
		WorkspaceRoot:    root,
		model:            "test-model",
		tokenMode:        boot.TokenModeFull,
		mode:             "normal",
		toolApprovalMode: control.ToolApprovalAuto,
		disabledMCP:      map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	defer func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
		app.releaseTabSharedHost(tab)
		tab.releaseSessionLease()
	}()

	app.buildTabController(tab)
	if tab.StartupErr != "" {
		t.Fatalf("build tab controller: %s", tab.StartupErr)
	}
	ctrl, ok := tab.Ctrl.(*control.Controller)
	if !ok || ctrl == nil {
		t.Fatalf("tab controller = %T, want *control.Controller", tab.Ctrl)
	}
	if ctrl.RecoveryCheckpointEnabled() {
		t.Fatal("desktop runtime configuration overrode project auto_recovery_checkpoint=off")
	}

	ctrl.SetFreshSessionPath(filepath.Join(ctrl.SessionDir(), "fresh-auto-guard-off.jsonl"))
	if ctrl.RecoveryCheckpointEnabled() {
		t.Fatal("desktop fresh-session rotation overrode project auto_recovery_checkpoint=off")
	}
}
