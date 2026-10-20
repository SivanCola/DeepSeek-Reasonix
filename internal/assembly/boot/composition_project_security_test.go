package boot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/contract/agentcomposition"
	"reasonix/internal/contract/provider"
	"reasonix/internal/ext/hook"
	"reasonix/internal/state/agentpreset"
)

func TestProjectCompositionCannotActivateUnauthorizedProgramsOrChooseDefault(t *testing.T) {
	for _, kind := range []string{"mcp", "hooks", "extensions"} {
		t.Run(kind, func(t *testing.T) {
			recorder := &effectRecordingProvider{}
			providerKind := "composition-project-security-" + kind
			provider.Register(providerKind, func(provider.Config) (provider.Provider, error) { return recorder, nil })
			home, root := compositionTestWorkspace(t, providerKind)
			marker := filepath.Join(root, "unauthorized-started")
			program := filepath.Join(root, "unauthorized.sh")
			if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf started > '"+marker+"'\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			mcp, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"unapproved": map[string]any{"command": program}}})
			writeFile(t, root, ".mcp.json", string(mcp))
			hooks := hook.Settings{Hooks: map[hook.Event][]hook.HookConfig{hook.SessionStart: {{Command: program}}}}
			raw, _ := json.Marshal(hooks)
			writeFile(t, root, filepath.Join(hook.SettingsDirname, hook.SettingsFilename), string(raw))
			store := agentpreset.New(home, root)
			d := agentcomposition.Builtins()[0]
			d.ID = "project:unapproved"
			empty := agentcomposition.Selection{Mode: agentcomposition.Select, Members: []agentcomposition.Module{}}
			d.MCP, d.Hooks, d.Extensions = empty, empty, empty
			id := "unapproved"
			if kind == "hooks" {
				id = fmt.Sprintf("%s:%s:0", hook.ScopeProject, hook.SessionStart)
			}
			selected := agentcomposition.Selection{Mode: agentcomposition.Select, Members: []agentcomposition.Module{{ID: id}}}
			switch kind {
			case "mcp":
				d.MCP = selected
			case "hooks":
				d.Hooks = selected
			case "extensions":
				d.Extensions = selected
			}
			if _, err := store.Save(d, ""); err != nil {
				t.Fatal(err)
			}
			writeFile(t, root, ".reasonix/agent-presets/settings.json", `{"default":"project:unapproved"}`)
			if got, err := store.Default(); err != nil || got != "builtin:general" {
				t.Fatalf("repository selected a default: %q %v", got, err)
			}
			ctrl, err := Build(t.Context(), Options{Home: home, WorkspaceRoot: root, resolvedShell: pinnedEffectShell()})
			if err != nil {
				t.Fatal(err)
			}
			if ctrl.CompositionBinding() != nil {
				ctrl.Close()
				t.Fatal("repository bound an unselected session")
			}
			ctrl.EnsureSessionPath()
			if err := ctrl.RunTurn(t.Context(), "Say hello."); err != nil {
				ctrl.Close()
				t.Fatal(err)
			}
			if len(recorder.requests()) == 0 {
				ctrl.Close()
				t.Fatal("unbound session never reached its provider")
			}
			ctrl.Close()
			ctrl, err = Build(t.Context(), Options{Home: home, WorkspaceRoot: root, AgentComposition: d.ID, resolvedShell: pinnedEffectShell()})
			if ctrl != nil {
				ctrl.Close()
			}
			if !errors.Is(err, agentcomposition.ErrUnavailable) {
				t.Fatalf("unauthorized %s selection was not refused: %v", kind, err)
			}
			var diagnostic *agentcomposition.DiagnosticError
			if !errors.As(err, &diagnostic) || len(diagnostic.Diagnostics) != 1 || diagnostic.Diagnostics[0].Module != kind+":"+id {
				t.Fatalf("wrong missing authority: %v", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("unauthorized %s started a program: %v", kind, err)
			}
		})
	}
}
