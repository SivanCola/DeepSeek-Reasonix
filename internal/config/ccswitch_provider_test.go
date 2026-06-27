package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadCCSwitchProviderCandidatesFromSQLite(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "cc-switch.db")
	createCCSwitchProviderDB(t, dbPath)
	sqliteExec(t, dbPath, `INSERT INTO providers (id, app_type, name, settings_config, provider_type) VALUES (
		'openai-id',
		'codex',
		'Work Gateway',
		'{"env":{"OPENAI_MODEL":"gpt-5"},"auth":{"OPENAI_API_KEY":"sk-openai"}}',
		''
	);
	INSERT INTO provider_endpoints (provider_id, app_type, url) VALUES ('openai-id', 'codex', 'https://gateway.example.test/v1');`)

	got, err := loadCCSwitchProviderCandidatesFromRoot(root)
	if err != nil {
		t.Fatalf("load candidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1: %+v", len(got), got)
	}
	c := got[0]
	if c.Kind != "openai" || c.BaseURL != "https://gateway.example.test/v1" || c.TargetName != "ccswitch-work-gateway" {
		t.Fatalf("candidate = %+v", c)
	}
	if !c.Importable || !c.Recommended || !c.KeyPresent {
		t.Fatalf("import state = importable:%v recommended:%v key:%v reasons:%v", c.Importable, c.Recommended, c.KeyPresent, c.Reasons)
	}
	if !reflect.DeepEqual(c.Models, []string{"gpt-5"}) {
		t.Fatalf("models = %+v, want gpt-5", c.Models)
	}
}

func TestLoadCCSwitchProviderLegacyFallback(t *testing.T) {
	root := t.TempDir()
	body := `{
		"claude": {
			"providers": {
				"proxy": {
					"name": "Claude Proxy",
					"settings": {
						"env": {
							"ANTHROPIC_BASE_URL": "https://claude-proxy.example.test",
							"ANTHROPIC_AUTH_TOKEN": "token-test",
							"ANTHROPIC_MODEL": "claude-sonnet-4-5"
						}
					}
				}
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadCCSwitchProviderCandidatesFromRoot(root)
	if err != nil {
		t.Fatalf("load legacy candidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1: %+v", len(got), got)
	}
	c := got[0]
	if c.Kind != "anthropic" || c.AuthScheme != "bearer" || c.APIKeyEnv != "REASONIX_CC_SWITCH_CLAUDE_PROXY_API_KEY" {
		t.Fatalf("candidate = %+v", c)
	}
	if !reflect.DeepEqual(c.Models, []string{"claude-sonnet-4-5"}) {
		t.Fatalf("models = %+v", c.Models)
	}
}

func TestLoadCCSwitchProviderEmptyDBDoesNotReadLegacyBackups(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "cc-switch.db")
	createCCSwitchProviderDB(t, dbPath)
	stale := `{"codex":{"providers":{"stale":{"name":"Stale","settings":{"env":{"OPENAI_BASE_URL":"https://stale.example/v1","OPENAI_MODEL":"gpt-5","OPENAI_API_KEY":"sk-stale"}}}}}}`
	if err := os.WriteFile(filepath.Join(root, "config.json.bak"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadCCSwitchProviderCandidatesFromRoot(root)
	if err != nil {
		t.Fatalf("load candidates: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty sqlite db should be authoritative, got: %+v", got)
	}
}

func TestProviderImportDeepSeekMergesOfficialAndKeyReplacement(t *testing.T) {
	isolateProviderImportHome(t)
	if _, err := SetCredential("DEEPSEEK_API_KEY", "old-key"); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	cfg := &Config{}
	candidates := []ProviderImportCandidate{{
		ID: "codex:deepseek", AppType: "codex", Name: "DeepSeek", Kind: "openai",
		BaseURL: "https://api.deepseek.com", Host: "api.deepseek.com",
		Models:  []string{"deepseek-v4-flash", "deepseek-v4-pro", "deepseek-chat"},
		Default: "deepseek-v4-flash", TargetName: "deepseek", APIKeyEnv: "DEEPSEEK_API_KEY",
		KeyPresent: true, Importable: true, Recommended: true, Status: ccSwitchProviderStatusReady,
		keyValue: "new-key",
	}}
	result, err := importCCSwitchProvidersIntoConfig(cfg, candidates, nil, false)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if result.Imported != 1 || result.KeyImported != 0 || result.KeySkipped != 1 {
		t.Fatalf("result = %+v", result)
	}
	if _, ok := cfg.Provider("ccswitch-deepseek"); ok {
		t.Fatal("created a custom ccswitch-deepseek provider")
	}
	p, ok := cfg.Provider("deepseek")
	if !ok {
		t.Fatal("missing official deepseek provider")
	}
	if p.BaseURL != "https://api.deepseek.com" || p.APIKeyEnv != "DEEPSEEK_API_KEY" || p.Prices["deepseek-v4-flash"] == nil {
		t.Fatalf("deepseek provider = %+v", p)
	}
	if !reflect.DeepEqual(p.Models, []string{"deepseek-v4-flash", "deepseek-v4-pro", "deepseek-chat"}) {
		t.Fatalf("models = %+v", p.Models)
	}
	if value, _, _ := storedCredentialValue("DEEPSEEK_API_KEY"); value != "old-key" {
		t.Fatalf("key overwritten without replace: %q", value)
	}
	result, err = importCCSwitchProvidersIntoConfig(cfg, candidates, nil, true)
	if err != nil {
		t.Fatalf("replace import: %v", err)
	}
	if result.KeyImported != 1 {
		t.Fatalf("replace result = %+v", result)
	}
	if value, _, _ := storedCredentialValue("DEEPSEEK_API_KEY"); value != "new-key" {
		t.Fatalf("key not replaced: %q", value)
	}
}

func TestProviderImportCustomOpenAIAndAnthropicBearer(t *testing.T) {
	isolateProviderImportHome(t)
	cfg := &Config{}
	candidates := []ProviderImportCandidate{
		{
			ID: "codex:work", AppType: "codex", Name: "Work", Kind: "openai",
			BaseURL: "https://gateway.example.test/v1", Host: "gateway.example.test",
			Models: []string{"gpt-5"}, Default: "gpt-5",
			TargetName: "ccswitch-work", APIKeyEnv: "REASONIX_CC_SWITCH_WORK_API_KEY",
			KeyPresent: true, Importable: true, Recommended: true, Status: ccSwitchProviderStatusReady,
			keyValue: "openai-key",
		},
		{
			ID: "claude:proxy", AppType: "claude", Name: "Claude Proxy", Kind: "anthropic",
			BaseURL: "https://claude-proxy.example.test", Host: "claude-proxy.example.test",
			Models: []string{"claude-sonnet-4-5"}, Default: "claude-sonnet-4-5",
			TargetName: "ccswitch-claude-proxy", APIKeyEnv: "REASONIX_CC_SWITCH_CLAUDE_PROXY_API_KEY", AuthScheme: "bearer",
			KeyPresent: true, Importable: true, Recommended: true, Status: ccSwitchProviderStatusReady,
			keyValue: "anthropic-token",
		},
	}
	result, err := importCCSwitchProvidersIntoConfig(cfg, candidates, nil, false)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if result.Imported != 2 || result.Added != 2 || result.KeyImported != 2 {
		t.Fatalf("result = %+v", result)
	}
	openai, ok := cfg.Provider("ccswitch-work")
	if !ok || openai.Kind != "openai" || openai.APIKeyEnv != "REASONIX_CC_SWITCH_WORK_API_KEY" {
		t.Fatalf("openai provider = %+v ok=%v", openai, ok)
	}
	anthropic, ok := cfg.Provider("ccswitch-claude-proxy")
	if !ok || anthropic.Kind != "anthropic" || anthropic.AuthScheme != "bearer" {
		t.Fatalf("anthropic provider = %+v ok=%v", anthropic, ok)
	}
	if !reflect.DeepEqual(cfg.Desktop.ProviderAccess, []string{"ccswitch-work", "ccswitch-claude-proxy"}) {
		t.Fatalf("provider access = %+v", cfg.Desktop.ProviderAccess)
	}
	if value, _, _ := storedCredentialValue("REASONIX_CC_SWITCH_CLAUDE_PROXY_API_KEY"); value != "anthropic-token" {
		t.Fatalf("anthropic token not stored: %q", value)
	}
}

func TestProviderImportCandidateMarksUnsupportedInvalidAndMissingKey(t *testing.T) {
	cases := []struct {
		name string
		src  ccSwitchProviderSource
		want string
	}{
		{
			name: "gemini",
			src:  ccSwitchProviderSource{ID: "g", AppType: "gemini", Name: "Gemini", SettingsConfig: `{"env":{"GEMINI_API_KEY":"key","GEMINI_MODEL":"gemini-2.5-pro"}}`},
			want: ccSwitchProviderStatusUnsupported,
		},
		{
			name: "missing base url",
			src:  ccSwitchProviderSource{ID: "o", AppType: "codex", Name: "OpenAI", SettingsConfig: `{"env":{"OPENAI_MODEL":"gpt-5"},"auth":{"OPENAI_API_KEY":"sk"}}`},
			want: ccSwitchProviderStatusInvalid,
		},
		{
			name: "missing model",
			src:  ccSwitchProviderSource{ID: "o", AppType: "codex", Name: "OpenAI", SettingsConfig: `{"env":{"OPENAI_BASE_URL":"https://api.example/v1"},"auth":{"OPENAI_API_KEY":"sk"}}`},
			want: ccSwitchProviderStatusInvalid,
		},
		{
			name: "missing key",
			src:  ccSwitchProviderSource{ID: "o", AppType: "codex", Name: "OpenAI", SettingsConfig: `{"env":{"OPENAI_BASE_URL":"https://api.example/v1","OPENAI_MODEL":"gpt-5"}}`},
			want: ccSwitchProviderStatusMissingKey,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := providerImportCandidateFromSource(tc.src)
			if got.Status != tc.want || got.Importable {
				t.Fatalf("candidate = %+v, want status %s and not importable", got, tc.want)
			}
		})
	}
}

func createCCSwitchProviderDB(t *testing.T, path string) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}
	sqliteExec(t, path, `CREATE TABLE providers (
		id TEXT PRIMARY KEY,
		app_type TEXT NOT NULL,
		name TEXT NOT NULL,
		settings_config TEXT,
		provider_type TEXT
	);
	CREATE TABLE provider_endpoints (
		provider_id TEXT NOT NULL,
		app_type TEXT NOT NULL,
		url TEXT NOT NULL
	);`)
}

func sqliteExec(t *testing.T, path, query string) {
	t.Helper()
	out, err := exec.Command("sqlite3", path, query).CombinedOutput()
	if err != nil {
		t.Fatalf("sqlite3 %s: %v\n%s", path, err, out)
	}
}

func isolateProviderImportHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("REASONIX_CREDENTIALS_STORE", "file")
	for _, key := range []string{
		"DEEPSEEK_API_KEY",
		"REASONIX_CC_SWITCH_WORK_API_KEY",
		"REASONIX_CC_SWITCH_CLAUDE_PROXY_API_KEY",
	} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
}

func TestProviderImportCandidateDoesNotExposeKeyInStringForm(t *testing.T) {
	c := providerImportCandidateFromSource(ccSwitchProviderSource{
		ID: "o", AppType: "codex", Name: "OpenAI",
		SettingsConfig: `{"env":{"OPENAI_BASE_URL":"https://api.example/v1","OPENAI_MODEL":"gpt-5"},"auth":{"OPENAI_API_KEY":"sk-secret-test"}}`,
	})
	if strings.Contains(strings.Join(c.Reasons, ","), "sk-secret-test") {
		t.Fatalf("candidate reasons leaked key: %+v", c.Reasons)
	}
}
