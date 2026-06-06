package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func legacyHome(t *testing.T) (src, dest, home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	return filepath.Join(home, ".reasonix", "config.json"), UserConfigPath(), home
}

func writeLegacy(t *testing.T, src, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateImportsKeyPluginsAndLang(t *testing.T) {
	src, dest, home := legacyHome(t)
	writeLegacy(t, src, `{
		"apiKey": "sk-legacy-123",
		"lang": "zh",
		"mcpServers": {
			"fs": {"command": "npx", "args": ["-y", "server-fs"], "type": "stdio"},
			"stripe": {"type": "http", "url": "https://mcp.stripe.com", "disabled": true}
		},
		"mcpEnv": {"fs": {"ROOT": "/tmp"}}
	}`)

	res, err := MigrateLegacyIfNeeded()
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if res == nil {
		t.Fatal("expected a migration result")
	}
	if !res.KeyToEnv || res.Plugins != 2 {
		t.Errorf("result = %+v, want KeyToEnv=true Plugins=2", res)
	}

	envData, err := os.ReadFile(UserCredentialsPath())
	if err != nil {
		t.Fatalf("read credentials: %v", err)
	}
	if !strings.Contains(string(envData), "DEEPSEEK_API_KEY=sk-legacy-123") {
		t.Errorf("credentials missing key: %q", envData)
	}
	if _, err := os.Stat(filepath.Join(home, ".env")); !os.IsNotExist(err) {
		t.Errorf("migration must not write the user's ~/.env, stat err=%v", err)
	}

	// Core config should have language but NOT plugins (they are in mcp.toml now)
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest config: %v", err)
	}
	toml := string(got)
	for _, want := range []string{`language      = "zh"`, `[desktop]`, `language = "zh"`} {
		if !strings.Contains(toml, want) {
			t.Errorf("dest config missing %q:\n%s", want, toml)
		}
	}
	// Plugins should NOT be in config.toml
	// Check mcp.toml has the plugins (not config.toml)

	mcpData, err := os.ReadFile(UserMCPConfigPath())
	if err != nil {
		t.Fatalf("read mcp.toml: %v", err)
	}
	mcpToml := string(mcpData)
	for _, want := range []string{`name    = "fs"`, `name    = "stripe"`, `type    = "http"`, `auto_start = false`} {
		if !strings.Contains(mcpToml, want) {
			t.Errorf("mcp.toml missing %q:\n%s", want, mcpToml)
		}
	}

	if _, err := os.Stat(src); err != nil {
		t.Errorf("legacy file must be left untouched: %v", err)
	}
}

func TestMigrateRoundTripsThroughLoad(t *testing.T) {
	src, _, _ := legacyHome(t)
	writeLegacy(t, src, `{"apiKey":"sk-x","mcpServers":{"fs":{"command":"npx","env":{"A":"1"}}}}`)

	if _, err := MigrateLegacyIfNeeded(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Plugins) != 1 || cfg.Plugins[0].Name != "fs" || cfg.Plugins[0].Command != "npx" {
		t.Errorf("plugins did not round-trip through Load: %+v", cfg.Plugins)
	}
	if cfg.Plugins[0].Env["A"] != "1" {
		t.Errorf("plugin env lost: %+v", cfg.Plugins[0].Env)
	}
}

func TestMigrateSkipsWhenDestExists(t *testing.T) {
	src, dest, _ := legacyHome(t)
	writeLegacy(t, src, `{"apiKey":"sk-x"}`)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("default_model = \"deepseek-flash\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := MigrateLegacyIfNeeded()
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if res != nil {
		t.Errorf("must not migrate over an existing v1+ config, got %+v", res)
	}
}

func TestMigrateImportsLegacyV1TOMLBeforeJSON(t *testing.T) {
	srcJSON, _, _ := legacyHome(t)
	legacyTOML := filepath.Join(filepath.Dir(UserConfigPath()), "reasonix.toml")
	if err := os.MkdirAll(filepath.Dir(legacyTOML), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyTOML, []byte(`
default_model = "deepseek-flash"
language = "en"

[ui]
theme = "light"
theme_style = "glacier"
close_behavior = "quit"

[[plugins]]
name = "legacy-v1"
command = "legacy-bin"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeLegacy(t, srcJSON, `{"apiKey":"sk-json-should-not-win"}`)

	res, err := MigrateLegacyIfNeeded()
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if res == nil || res.From != legacyTOML {
		t.Fatalf("expected v1 TOML migration, got %+v", res)
	}

	got, err := os.ReadFile(UserConfigPath())
	if err != nil {
		t.Fatalf("read migrated config: %v", err)
	}
	text := string(got)
	for _, want := range []string{`config_version = 2`, `[desktop]`, `close_behavior = "quit"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("migrated TOML missing %q:\n%s", want, text)
		}
	}
	// Plugins migrated to mcp.toml, not in config.toml as real entries
	mcpData, _ := os.ReadFile(UserMCPConfigPath())
	if !strings.Contains(string(mcpData), `name    = "legacy-v1"`) {
		t.Fatal("mcp.toml missing plugin:\n" + string(mcpData))
	}
	if _, err := os.Stat(UserCredentialsPath()); !os.IsNotExist(err) {
		t.Fatalf("v1 TOML migration should not import lower-priority JSON key, credentials stat err=%v", err)
	}
}

func TestMigrateImportsLegacyV1HomeTOMLBeforeJSON(t *testing.T) {
	srcJSON, _, home := legacyHome(t)
	legacyTOML := filepath.Join(home, ".reasonix", "reasonix.toml")
	if err := os.MkdirAll(filepath.Dir(legacyTOML), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyTOML, []byte(`
default_model = "deepseek-flash"

[[plugins]]
name = "legacy-home-v1"
command = "legacy-home-bin"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeLegacy(t, srcJSON, `{"apiKey":"sk-json-should-not-win","mcpServers":{"json":{"command":"json-bin"}}}`)

	res, err := MigrateLegacyIfNeeded()
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if res == nil || res.From != legacyTOML {
		t.Fatalf("expected home v1 TOML migration, got %+v", res)
	}

	mcpData, _ := os.ReadFile(UserMCPConfigPath())
	if !strings.Contains(string(mcpData), `name    = "legacy-home-v1"`) {
		t.Fatal("home v1 plugin was not migrated to mcp.toml:\n" + string(mcpData))
	}
	if strings.Contains(string(mcpData), `name    = "json"`) {
		t.Fatalf("lower-priority v0.5 JSON should not be merged when v1 TOML exists:\n%s", mcpData)
	}
}

func TestMigrateNoLegacyIsNoop(t *testing.T) {
	legacyHome(t)
	res, err := MigrateLegacyIfNeeded()
	if err != nil || res != nil {
		t.Errorf("no legacy install should be a silent no-op, got res=%+v err=%v", res, err)
	}
}

func TestImportTOMLWithoutSkillsPreservesInlineGlobalSkills(t *testing.T) {
	_, _, home := legacyHome(t)
	if err := os.MkdirAll(UserRootDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(UserConfigPath(), []byte(`
default_model = "deepseek-flash"

[skills]
paths = ["~/inline-skills"]
disabled_skills = ["review"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(home, "project.toml")
	if err := os.WriteFile(src, []byte(`
[[plugins]]
name = "imported"
command = "imported-bin"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	imported, skipped, errs := ImportConfigFromPath(src)
	if len(errs) > 0 {
		t.Fatalf("import errors: %v", errs)
	}
	if imported != 1 || skipped != 0 {
		t.Fatalf("imported=%d skipped=%d, want 1/0", imported, skipped)
	}
	if _, err := os.Stat(UserSkillsConfigPath()); !os.IsNotExist(err) {
		t.Fatalf("import without [skills] should not create skills.toml, stat err=%v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Skills.Paths) != 1 || cfg.Skills.Paths[0] != "~/inline-skills" {
		t.Fatalf("inline skills paths were not preserved: %+v", cfg.Skills.Paths)
	}
	if len(cfg.Skills.DisabledSkills) != 1 || cfg.Skills.DisabledSkills[0] != "review" {
		t.Fatalf("inline disabled skills were not preserved: %+v", cfg.Skills.DisabledSkills)
	}
}

func TestImportTOMLWithSkillsMergesExistingInlineGlobalSkills(t *testing.T) {
	_, _, home := legacyHome(t)
	if err := os.MkdirAll(UserRootDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(UserConfigPath(), []byte(`
default_model = "deepseek-flash"

[skills]
paths = ["~/inline-skills"]
disabled_skills = ["review"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(home, "project.toml")
	if err := os.WriteFile(src, []byte(`
[skills]
paths = ["~/imported-skills"]
disabled_skills = ["explore"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, errs := ImportConfigFromPath(src); len(errs) > 0 {
		t.Fatalf("import errors: %v", errs)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	paths := strings.Join(cfg.Skills.Paths, "\n")
	for _, want := range []string{"~/inline-skills", "~/imported-skills"} {
		if !strings.Contains(paths, want) {
			t.Fatalf("merged skills paths missing %q: %+v", want, cfg.Skills.Paths)
		}
	}
	disabled := strings.Join(cfg.Skills.DisabledSkills, "\n")
	for _, want := range []string{"review", "explore"} {
		if !strings.Contains(disabled, want) {
			t.Fatalf("merged disabled skills missing %q: %+v", want, cfg.Skills.DisabledSkills)
		}
	}
}

func TestMigrateToleratesUTF8BOM(t *testing.T) {
	src, _, _ := legacyHome(t)
	writeLegacy(t, src, string([]byte{0xEF, 0xBB, 0xBF})+`{"apiKey":"sk-bom"}`)
	res, err := MigrateLegacyIfNeeded()
	if err != nil {
		t.Fatalf("a BOM-prefixed legacy config must still parse: %v", err)
	}
	if res == nil || !res.KeyToEnv {
		t.Fatalf("BOM-prefixed config did not migrate: %+v", res)
	}
	data, _ := os.ReadFile(UserCredentialsPath())
	if !strings.Contains(string(data), "DEEPSEEK_API_KEY=sk-bom") {
		t.Errorf("key not migrated from BOM-prefixed config: %q", data)
	}
}

func TestMigrateCustomBaseURLWarns(t *testing.T) {
	src, _, _ := legacyHome(t)
	writeLegacy(t, src, `{"apiKey":"sk-x","baseUrl":"https://my-proxy.example/v1"}`)
	res, err := MigrateLegacyIfNeeded()
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Error("a non-DeepSeek base_url should produce a warning")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load migrated config: %v", err)
	}
	for _, name := range []string{"deepseek-flash", "deepseek-pro"} {
		p, ok := cfg.Provider(name)
		if !ok || p.BaseURL != "https://my-proxy.example/v1" {
			t.Fatalf("%s base_url was not migrated: %+v", name, p)
		}
	}
}
