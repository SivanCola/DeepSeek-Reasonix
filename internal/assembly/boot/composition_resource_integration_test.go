package boot

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/contract/agentcomposition"
	"reasonix/internal/ext/hook"
	"reasonix/internal/ext/installsource"
	"reasonix/internal/ext/plugin"
	"reasonix/internal/ext/pluginpkg"
	"reasonix/internal/state/agentpreset"
)

func resourcePreset(t *testing.T, home, root string) Options {
	t.Helper()
	d := agentcomposition.Builtins()[0]
	d.ID = "user:audit"
	if _, err := agentpreset.New(home, root).Save(d, ""); err != nil {
		t.Fatal(err)
	}
	return Options{Home: home, WorkspaceRoot: root, AgentComposition: d.ID, resolvedShell: pinnedEffectShell()}
}

func TestCompositionLinkedExtensionResources(t *testing.T) {
	for _, internalLink := range []bool{false, true} {
		name := "plain-package"
		if internalLink {
			name = "internal-link"
		}
		t.Run(name, func(t *testing.T) {
			home, root := compositionTestWorkspace(t, "openai")
			t.Setenv("REASONIX_HOME", home)
			t.Setenv("REASONIX_STATE_HOME", home)
			source := filepath.Join(root, "extension")
			writeFile(t, source, "reasonix-plugin.json", `{"apiVersion":"reasonix.io/plugin/v2","name":"audit","version":"1.0.0","contributes":{"skills":["skills"]}}`)
			writeFile(t, source, "skills/check/SKILL.md", "---\nname: check\ndescription: audit skill\n---\nCHECK")
			writeFile(t, source, "notes.txt", "NOTES")
			if internalLink {
				if err := os.Symlink("notes.txt", filepath.Join(source, "notes-alias.txt")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			tl := installsource.NewTool(installsource.Options{ProjectRoot: root})
			args, _ := json.Marshal(map[string]any{"source": source, "kind": "plugin", "mode": "link", "scope": "global", "apply": true})
			out, err := tl.Execute(t.Context(), args)
			if err != nil {
				t.Fatalf("install: %v %s", err, out)
			}
			var response struct {
				OK bool `json:"ok"`
			}
			if err := json.Unmarshal([]byte(out), &response); err != nil || !response.OK {
				t.Fatalf("install failed: %v %s", err, out)
			}
			installed, warnings := pluginpkg.LoadInstalled(home)
			if len(installed) != 1 {
				t.Fatalf("installed=%v warnings=%v", installed, warnings)
			}
			info, err := os.Lstat(installed[0].Package.Root)
			if err != nil || !info.IsDir() {
				t.Fatalf("linked install did not register real root: %v", err)
			}
			ordinary, err := BuildRuntime(t.Context(), Options{Home: home, WorkspaceRoot: root, resolvedShell: pinnedEffectShell()})
			if err != nil {
				t.Fatal(err)
			}
			ordinary.Controller.Close()
			opts := resourcePreset(t, home, root)
			bound, err := BuildRuntime(t.Context(), opts)
			if err != nil {
				t.Fatalf("linked extension was rejected: %v", err)
			}
			defer bound.Controller.Close()
			writeFile(t, source, "notes.txt", "CHANGED")
			opts.AgentComposition, opts.Composition = "", bound.Controller.CompositionBinding()
			restored, err := BuildRuntime(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Controller.Close()
			packages, err := resource[retainedExtension](restored.Controller.CompositionBinding(), "extension")
			if err != nil || len(packages) != 1 {
				t.Fatalf("retained extension missing: %v", err)
			}
			for _, file := range packages[0].Assets.Files {
				if strings.HasPrefix(file.Path, "notes") && string(file.Data) != "NOTES" {
					t.Fatal("retained link followed a source edit")
				}
			}
			if bound.Snapshot.CacheHash() != restored.Snapshot.CacheHash() {
				t.Fatal("restoring linked extension changed provider prefix")
			}
		})
	}
}

func TestCompositionLinkedHookResources(t *testing.T) {
	home, root := compositionTestWorkspace(t, "openai")
	t.Setenv("REASONIX_HOME", home)
	t.Setenv("REASONIX_STATE_HOME", home)
	source := filepath.Join(home, "real-scripts")
	writeFile(t, source, "entry.sh", "HOOK BODY")
	link := filepath.Join(home, "linked-scripts")
	if err := os.Symlink(source, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	settings := hook.Settings{Hooks: map[hook.Event][]hook.HookConfig{hook.SessionStart: {{Command: "cat '" + filepath.Join(link, "entry.sh") + "'"}}}}
	raw, _ := json.Marshal(settings)
	writeFile(t, home, hook.SettingsFilename, string(raw))
	ordinary, err := BuildRuntime(t.Context(), Options{Home: home, WorkspaceRoot: root, resolvedShell: pinnedEffectShell()})
	if err != nil {
		t.Fatal(err)
	}
	defer ordinary.Controller.Close()
	if got := ordinary.Controller.HookRunner().SessionStart(t.Context()); len(got) != 1 || got[0] != "HOOK BODY" {
		t.Fatalf("ordinary hook failed: %v", got)
	}
	opts := resourcePreset(t, home, root)
	bound, err := BuildRuntime(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer bound.Controller.Close()
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	opts.AgentComposition, opts.Composition = "", bound.Controller.CompositionBinding()
	restored, err := BuildRuntime(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Controller.Close()
	if got := restored.Controller.HookRunner().SessionStart(t.Context()); len(got) != 1 || got[0] != "HOOK BODY" {
		t.Fatalf("restored hook failed: %v", got)
	}
}

func TestCompositionUnavailableMCPNamesDependency(t *testing.T) {
	home, root := compositionTestWorkspace(t, "openai")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("private-fixture-token"))
	}))
	defer server.Close()
	opts := Options{Home: home, WorkspaceRoot: root, resolvedShell: pinnedEffectShell(), ExtraPlugins: []plugin.Spec{{
		Name: "audit", Type: "http", URL: server.URL, Authorized: true,
	}}}
	ordinary, err := BuildRuntime(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	ordinary.Controller.Close()
	opts.AgentComposition = resourcePreset(t, home, root).AgentComposition
	bound, err := BuildRuntime(t.Context(), opts)
	if bound != nil {
		bound.Controller.Close()
	}
	if !errors.Is(err, agentcomposition.ErrUnavailable) {
		t.Fatalf("unexpected composition result: %v", err)
	}
	var diagnostic *agentcomposition.DiagnosticError
	if !errors.As(err, &diagnostic) || len(diagnostic.Diagnostics) != 1 {
		t.Fatalf("MCP failure lost resource identity: %v", err)
	}
	d := diagnostic.Diagnostics[0]
	if d.Code != "agent_composition.mcp_unavailable" || d.Module != "audit" || !strings.Contains(d.Detail, "401") {
		t.Fatalf("MCP failure lost status: %+v", d)
	}
	if strings.Contains(err.Error(), "private-fixture-token") || strings.Contains(d.Detail, "private-fixture-token") {
		t.Fatal("MCP response body leaked through the diagnostic")
	}
}
