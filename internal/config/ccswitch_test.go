package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCCSwitchRowsToPlugins(t *testing.T) {
	rows := []ccSwitchMCPRow{
		{Name: "docs", ServerConfig: `{"type":"http","url":"https://mcp.example.test","headers":{"Authorization":"Bearer ${TOKEN}"}}`},
		{Name: "fs", ServerConfig: `{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","."]}`},
	}
	got, err := ccSwitchRowsToPlugins(rows)
	if err != nil {
		t.Fatalf("ccSwitchRowsToPlugins: %v", err)
	}
	if got[0].Name != "docs" || got[0].Type != "http" || got[0].URL != "https://mcp.example.test" {
		t.Fatalf("http entry = %+v", got[0])
	}
	if got[0].Headers["Authorization"] != "Bearer ${TOKEN}" {
		t.Errorf("header was not preserved: %+v", got[0].Headers)
	}
	if got[0].AutoStart == nil || !*got[0].AutoStart {
		t.Errorf("http import should auto-start by default: %+v", got[0].AutoStart)
	}
	if got[1].Name != "fs" || got[1].Command != "npx" ||
		!reflect.DeepEqual(got[1].Args, []string{"-y", "@modelcontextprotocol/server-filesystem", "."}) {
		t.Fatalf("stdio entry = %+v", got[1])
	}
}

func TestCCSwitchImportMarksHeavyMCPNoAutoStart(t *testing.T) {
	rows := []ccSwitchMCPRow{
		{Name: "@modelcontextprotocol/server-chrome-devtools", ServerConfig: `{"command":"npx","args":["-y","chrome-devtools-mcp@latest"]}`},
		{Name: "legacy", ServerConfig: `{"type":"sse","url":"https://example.test/sse"}`},
	}
	got, err := ccSwitchRowsToPlugins(rows)
	if err != nil {
		t.Fatalf("ccSwitchRowsToPlugins: %v", err)
	}
	for _, e := range got {
		if e.AutoStart == nil || *e.AutoStart {
			t.Fatalf("%s should import with auto_start=false, got %+v", e.Name, e.AutoStart)
		}
	}
}

func TestLoadCCSwitchLegacyConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json.migrated")
	body := `{
		"mcp": {
			"servers": {
				"off": {
					"name": "off",
					"server": {"command": "node", "args": ["off.js"]},
					"apps": {"codex": false}
				},
				"time": {
					"name": "@modelcontextprotocol/server-time",
					"server": {"type":"stdio", "command": "uvx", "args": ["mcp-server-time"]},
					"apps": {"codex": true}
				}
			}
		}
	}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadCCSwitchLegacyConfig(path)
	if err != nil {
		t.Fatalf("loadCCSwitchLegacyConfig: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("entries = %d, want 1: %+v", len(got), got)
	}
	if got[0].Name != "@modelcontextprotocol/server-time" || got[0].Command != "uvx" {
		t.Fatalf("entry = %+v", got[0])
	}
}

func TestLoadCCSwitchMCPEmptyDBDoesNotReadLegacyBackups(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}
	root := t.TempDir()
	dbPath := filepath.Join(root, "cc-switch.db")
	if out, err := exec.Command("sqlite3", dbPath, `CREATE TABLE mcp_servers (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		server_config TEXT NOT NULL,
		enabled_codex BOOLEAN NOT NULL DEFAULT 0
	);`).CombinedOutput(); err != nil {
		t.Fatalf("create sqlite db: %v\n%s", err, out)
	}
	stale := `{"mcp":{"servers":{"stale":{"name":"stale","server":{"command":"node","args":["stale.js"]},"apps":{"codex":true}}}}}`
	if err := os.WriteFile(filepath.Join(root, "config.json.migrated"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadCCSwitchMCPFromRoot(root)
	if err != nil {
		t.Fatalf("loadCCSwitchMCPFromRoot: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty sqlite db should be authoritative, got legacy entries: %+v", got)
	}
}
