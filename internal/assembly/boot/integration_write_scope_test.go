package boot

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/safety/permission"
	"reasonix/internal/session/control"
	"reasonix/internal/tools/builtin"
)

func TestIntegrationSourceChecksRequireGrantedScopeForManagedConfig(t *testing.T) {
	repo := t.TempDir()
	workspace := filepath.Join(repo, "app")
	if err := os.Mkdir(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(repo, "config.toml")
	if err := os.WriteFile(configPath, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	gate := control.NewSharedHeadlessGate(permission.Policy{}, control.ToolApprovalYolo)
	ws := builtin.Workspace{Dir: workspace, ManagedConfig: builtin.NewManagedConfigPaths([]string{configPath})}
	if err := checkSourceMutationPaths(t.Context(), workspace, gate, ws.TargetAccessCheck(), []string{configPath}); err == nil {
		t.Fatal("integration borrowed a preview-only managed config exception")
	}
	ws.WriteRoots = []string{workspace, repo}
	if err := checkSourceMutationPaths(t.Context(), workspace, gate, ws.TargetAccessCheck(), []string{configPath}); err != nil {
		t.Fatalf("explicit source write scope denied: %v", err)
	}
}

func TestIntegrationSourceChecksRespectAbsoluteAndRelativeDenyRules(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "protected.txt")
	for _, pattern := range []string{"edit_file(protected.txt)", "edit_file(" + filepath.ToSlash(path) + ")"} {
		gate := control.NewSharedHeadlessGate(permission.New("allow", nil, nil, []string{pattern}), control.ToolApprovalYolo)
		access := (builtin.Workspace{Dir: root, WriteRoots: []string{root}}).TargetAccessCheck()
		if err := checkSourceMutationPaths(t.Context(), root, gate, access, []string{path}); err == nil {
			t.Fatalf("integration bypassed %s", pattern)
		}
	}
}
