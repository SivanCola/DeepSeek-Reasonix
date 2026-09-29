package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestLegacyProjectDeclarationsAreNotGrantsOrLoadFailures(t *testing.T) {
	rules := make([]string, 39)
	for i := range rules {
		rules[i] = fmt.Sprintf("Bash=echo private-command-%d", i)
	}
	project := "# preserved comment\n[model_roles]\nanswerer = 'unchanged'\n[permissions]\nallow = " + renderStringArray(rules) + "\n"
	cfg, root := loadScoped(t, "[permissions]\nmode = 'ask'\ndeny = ['Bash(rm:*)']\n", project)
	file := filepath.Join(root, "reasonix.toml")
	before, _ := os.Stat(file)
	if cfg.HasLoadWarnings() || len(cfg.Permissions.Allow) != 0 {
		t.Fatalf("warnings=%v grants=%v", cfg.LoadWarnings(), cfg.Permissions.Allow)
	}
	groups := cfg.DiagnosticGroups()
	if len(groups) != 1 || groups[0].Count != 39 || groups[0].Status != "on_demand" || groups[0].Source != file {
		t.Fatalf("groups=%+v", groups)
	}
	raw, _ := json.Marshal(groups)
	if strings.Contains(string(raw), "private-command") {
		t.Fatalf("diagnostics exported command bodies: %s", raw)
	}
	for range 2 {
		if _, err := LoadForRootReadOnly(root); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := os.Stat(file)
	content, _ := os.ReadFile(file)
	if string(content) != project || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("read-only compatibility rewrote project configuration")
	}
	if !slices.Contains(cfg.Permissions.Deny, "Bash(rm:*)") {
		t.Fatal("lost deny rule")
	}
}

func TestExistingProjectGrantsCoverDeclarationsAndDoNotResurrect(t *testing.T) {
	out := t.TempDir()
	rule := "Bash=echo exact $(value)"
	_, root := loadScoped(t, "", "[permissions]\nallow = "+renderStringArray([]string{rule})+"\n[sandbox]\nallow_write = ["+tomlQuote(filepath.Join(out, "child"))+"]\n")
	store := NewProjectGrantStore(ReasonixHomeDir())
	if err := store.Update(root, func(g ProjectGrant) (ProjectGrant, error) {
		g.Allow = []string{rule}
		g.AllowWrite = []string{out}
		return g, nil
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadForRootReadOnly(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Diagnostics()) != 0 || !slices.Equal(cfg.Permissions.Allow, []string{rule}) || !slices.Equal(cfg.Sandbox.AllowWrite, []string{out}) {
		t.Fatalf("effective=%+v diagnostics=%+v", cfg.Sandbox, cfg.Diagnostics())
	}
	other := t.TempDir()
	otherCfg, err := LoadForRootReadOnly(other)
	if err != nil || len(otherCfg.Permissions.Allow) != 0 {
		t.Fatal("grant crossed workspace boundary", err)
	}
	if err := store.Update(root, func(g ProjectGrant) (ProjectGrant, error) { return ProjectGrant{}, nil }); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadForRootReadOnly(root)
	if err != nil || len(cfg.Permissions.Allow) != 0 || len(cfg.Sandbox.AllowWrite) != 0 || len(cfg.DiagnosticGroups()) != 2 {
		t.Fatalf("revoked grant restored: %v %+v", err, cfg.DiagnosticGroups())
	}
}

func TestCorruptProjectGrantsAreVisible(t *testing.T) {
	_, root := loadScoped(t, "", "")
	path := NewProjectGrantStore(ReasonixHomeDir()).Path()
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	view := InspectDiagnostics("local", root)
	if len(view.Items) != 1 || view.Items[0].Code != "project_grants_unavailable" || view.Items[0].Severity != "warning" {
		t.Fatalf("view=%+v", view)
	}
}

func TestEquivalentProjectRootUsesExistingUserValue(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	userPath := filepath.Clean(parent)
	projectPath := parent + string(filepath.Separator) + "."
	if runtime.GOOS == "windows" {
		projectPath = filepath.ToSlash(parent)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[sandbox]\nworkspace_root = "+tomlQuote(userPath)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "reasonix.toml"), []byte("[sandbox]\nworkspace_root = "+tomlQuote(projectPath)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadForRootReadOnly(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Diagnostics()) != 0 || cfg.Sandbox.WorkspaceRoot != userPath {
		t.Fatalf("root=%q diagnostics=%+v", cfg.Sandbox.WorkspaceRoot, cfg.Diagnostics())
	}
}

func TestDiagnosticEmptyAndUnavailableSnapshots(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	view := InspectDiagnostics("host-a", t.TempDir())
	raw, _ := json.Marshal(view)
	if view.Status != "ready" || view.Items == nil || !strings.Contains(string(raw), `"items":[]`) {
		t.Fatalf("view=%s", raw)
	}
	if InspectDiagnostics("host-b", "").Status != "unavailable" {
		t.Fatal("missing workspace looked healthy")
	}
}

func TestDiagnosticDetailsAreExplicitRedactedAndOrderIndependent(t *testing.T) {
	rules := []string{"Bash=echo first", "Bash=echo API_KEY=sk-proj-abcdefghijklmnop123456"}
	_, root := loadScoped(t, "", "[permissions]\nallow="+renderStringArray(rules)+"\n")
	view := InspectDiagnostics("local", root)
	id := view.Items[0].ID
	if view.Items[0].Values != nil {
		t.Fatal("background snapshot includes values")
	}
	detail := InspectDiagnosticDetails("local", root, id)
	raw, _ := json.Marshal(detail)
	if len(detail.Items[0].Values) != 2 || strings.Contains(string(raw), "abcdefghijklmnop123456") {
		t.Fatalf("unsafe detail: %s", raw)
	}
	slices.Reverse(rules)
	if err := os.WriteFile(filepath.Join(root, "reasonix.toml"), []byte("[permissions]\nallow="+renderStringArray(rules)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if InspectDiagnostics("local", root).Items[0].ID != id {
		t.Fatal("reordering changed issue identity")
	}
	if InspectDiagnosticDetails("local", root, "stale-id").Items[0].Values != nil {
		t.Fatal("stale issue disclosed unrelated values")
	}
}

func TestDiagnosticIDsAreNotAnUnkeyedDigestOfValues(t *testing.T) {
	rule := "Bash=echo API_KEY=sk-proj-abcdefghijklmnop123456"
	_, root := loadScoped(t, "", "[permissions]\nallow="+renderStringArray([]string{rule})+"\n")
	d := InspectDiagnostics("local", root).Items[0]
	for _, guess := range []string{rule, d.Scope + "\x00" + d.Source + "\x00" + d.Field + "\x00" + d.Code + "\x00" + rule} {
		if sum := sha256.Sum256([]byte(guess)); d.ID == hex.EncodeToString(sum[:]) {
			t.Fatalf("diagnostic ID confirms the declared value %q", guess)
		}
	}
	if again := InspectDiagnostics("local", root).Items[0].ID; again != d.ID {
		t.Fatalf("ID changed within one process: %s != %s", again, d.ID)
	}
}

func TestInheritedUserValuesDoNotBecomeProjectDiagnostics(t *testing.T) {
	cfg, _ := loadScoped(t, "[sandbox]\nallow_write=['${REASONIX_NONEXISTENT_DIAGNOSTIC_VAR}/cache']\n", "")
	if len(cfg.DiagnosticGroups()) != 0 {
		t.Fatalf("attributed inherited user value to project: %+v", cfg.DiagnosticGroups())
	}
}

func TestDiagnosticPathIdentityAndBoundary(t *testing.T) {
	parent := t.TempDir()
	root, sibling := filepath.Join(parent, "repo"), filepath.Join(parent, "repo-other")
	for _, p := range []string{root, sibling} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	c := Default()
	if c.authorizedPath(root, []string{root}, sibling) {
		t.Fatal("string prefix granted sibling")
	}
	if !c.authorizedPath(root, []string{root}, filepath.Join(root, "missing", "child")) {
		t.Fatal("missing tail lost its authorized ancestor")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(sibling, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if c.authorizedPath(root, []string{root}, filepath.Join(link, "new")) {
		t.Fatal("external link granted by project root")
	}
	if !c.authorizedPath(root, []string{sibling}, filepath.Join(link, "new")) {
		t.Fatal("existing external grant not recognized")
	}
	upper := filepath.Join(parent, "REPO")
	if err := os.Mkdir(upper, 0700); err == nil {
		if c.equivalentConfigPath(root, root, upper) {
			t.Fatal("distinct case-sensitive directories conflated")
		}
	} else if os.IsExist(err) && !c.equivalentConfigPath(root, root, upper) {
		t.Fatal("case-insensitive directory identity lost")
	}
}
func TestDiagnosticPermissionCoverageRetainsRuleSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, global, project string
		pending               bool
	}{
		{"exact", "Bash=echo one", "Bash=echo one", false},
		{"glob", "Bash(go test:*)", "Bash=go test ./...", false},
		{"tool", "Read", "Read(src/**)", false},
		{"no_broadening", "Bash=echo one", "Bash", true},
		{"literal_star", "Bash=echo *", "Bash=echo secret", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _ := loadScoped(t, "[permissions]\nmode='ask'\nallow="+renderStringArray([]string{tc.global})+"\ndeny=['Bash(rm:*)']\nask=['Write']\n", "[permissions]\nallow="+renderStringArray([]string{tc.project})+"\n")
			if len(cfg.DiagnosticGroups()) > 0 != tc.pending {
				t.Fatalf("diagnostics=%+v", cfg.DiagnosticGroups())
			}
			if !slices.Equal(cfg.Permissions.Allow, []string{tc.global}) || !slices.Contains(cfg.Permissions.Ask, "Write") || !slices.Contains(cfg.Permissions.Deny, "Bash(rm:*)") || cfg.Permissions.Mode != "ask" {
				t.Fatalf("permission semantics changed: %+v", cfg.Permissions)
			}
		})
	}
}
func TestDiagnosticExistingWorkspaceRootCoversExternalDeclaration(t *testing.T) {
	root := t.TempDir()
	cfg, _ := loadScoped(t, "[sandbox]\nworkspace_root="+tomlQuote(root)+"\n", "[sandbox]\nallow_write=["+tomlQuote(filepath.Join(root, "child"))+"]\n")
	if len(cfg.Diagnostics()) != 0 || len(cfg.Sandbox.AllowWrite) != 0 {
		t.Fatalf("existing root not recognized: %+v", cfg.Diagnostics())
	}
	// Declaring an unapproved workspace root must not bootstrap authorization.
	cfg, _ = loadScoped(t, "", "[sandbox]\nworkspace_root="+tomlQuote(root)+"\nallow_write=["+tomlQuote(filepath.Join(root, "child"))+"]\n")
	if len(cfg.DiagnosticGroups()) != 2 || len(cfg.Sandbox.AllowWrite) != 0 {
		t.Fatal("project root granted its own write declaration")
	}
}
func TestDiagnosticGlobalFailureRemainsHostScoped(t *testing.T) {
	cfg, _ := loadScoped(t, "[broken", "")
	if len(cfg.Diagnostics()) == 0 {
		t.Fatal("missing load failure")
	}
	for _, item := range cfg.Diagnostics() {
		if item.Scope != "user" || item.Source != filepath.Join(ReasonixHomeDir(), "config.toml") {
			t.Fatalf("global failure attributed to project: %+v", item)
		}
	}
}
