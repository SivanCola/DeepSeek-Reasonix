package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const ccSwitchDir = ".cc-switch"

type ccSwitchMCPRow struct {
	Name         string `json:"name"`
	ServerConfig string `json:"server_config"`
}

type ccSwitchLegacyServer struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Server mcpServerSpec `json:"server"`
	Apps   struct {
		Codex bool `json:"codex"`
	} `json:"apps"`
}

type MCPImportCandidate struct {
	Entry       PluginEntry
	Recommended bool
	Reasons     []string
}

func LoadCCSwitchMCPCandidates() ([]MCPImportCandidate, error) {
	entries, err := LoadCCSwitchMCP()
	if err != nil {
		return nil, err
	}
	candidates := make([]MCPImportCandidate, len(entries))
	for i, e := range entries {
		candidates[i] = classifyMCPImportCandidate(e)
	}
	return candidates, nil
}

// LoadCCSwitchMCP reads MCP servers enabled for Codex from cc-switch and maps
// them to Reasonix plugin entries. Newer cc-switch stores servers in SQLite;
// older installs kept them in config.json(.migrated/.bak), so we support both.
func LoadCCSwitchMCP() ([]PluginEntry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cc-switch import: resolve home: %w", err)
	}
	return loadCCSwitchMCPFromRoot(filepath.Join(home, ccSwitchDir))
}

func loadCCSwitchMCPFromRoot(root string) ([]PluginEntry, error) {
	dbPath := filepath.Join(root, "cc-switch.db")
	if _, err := os.Stat(dbPath); err == nil {
		entries, err := loadCCSwitchMCPDB(dbPath)
		if err != nil {
			return nil, err
		}
		return entries, nil
	} else if !os.IsNotExist(err) {
		// A present but unreadable/corrupt database should be visible to the user.
		return nil, err
	}
	for _, name := range []string{"config.json", "config.json.migrated", "config.json.bak"} {
		entries, err := loadCCSwitchLegacyConfig(filepath.Join(root, name))
		if err == nil && len(entries) > 0 {
			return entries, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("cc-switch import: no Codex-enabled MCP servers found in %s", root)
}

func ImportCCSwitchMCPEntries(entries []PluginEntry) (total, added, updated, disabled int, err error) {
	cfg, err := Load()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	return importMCPEntries(cfg, entries)
}

// ImportCCSwitchMCP upserts cc-switch's Codex-enabled MCP servers into the
// active Reasonix config and saves it.
func ImportCCSwitchMCP() (total, added, updated, disabled int, err error) {
	entries, err := LoadCCSwitchMCP()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	cfg, err := Load()
	if err != nil {
		return 0, 0, 0, 0, err
	}
	return importMCPEntries(cfg, entries)
}

func importMCPEntries(cfg *Config, entries []PluginEntry) (total, added, updated, disabled int, err error) {
	existing := make(map[string]PluginEntry, len(cfg.Plugins))
	for _, p := range cfg.Plugins {
		existing[p.Name] = p
	}
	for _, e := range entries {
		if prev, ok := existing[e.Name]; ok {
			updated++
			if prev.AutoStart != nil {
				e.AutoStart = prev.AutoStart
			}
		} else {
			added++
		}
		if !e.ShouldAutoStart() {
			disabled++
		}
		if err := cfg.UpsertPlugin(e); err != nil {
			return 0, 0, 0, 0, err
		}
		existing[e.Name] = e
	}
	if err := cfg.Save(); err != nil {
		return 0, 0, 0, 0, err
	}
	return len(entries), added, updated, disabled, nil
}

func loadCCSwitchMCPDB(path string) ([]PluginEntry, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, fmt.Errorf("cc-switch import: sqlite3 not found to read %s", path)
	}
	columns, err := readSQLiteColumns(sqlite, path, "mcp_servers")
	if err != nil {
		return nil, fmt.Errorf("cc-switch import: inspect %s: %w", path, err)
	}
	configColumn := ""
	switch {
	case columns["server"]:
		configColumn = "server"
	case columns["server_config"]:
		configColumn = "server_config"
	default:
		return nil, fmt.Errorf("cc-switch import: mcp_servers table has no server or server_config column")
	}
	nameColumn := "id"
	if columns["name"] {
		nameColumn = "name"
	}
	where := ""
	if columns["enabled_codex"] {
		where = " WHERE enabled_codex = 1"
	}
	order := nameColumn
	if nameColumn == "name" && columns["id"] {
		order = "name, id"
	}
	query := fmt.Sprintf(`SELECT %s AS name, %s AS server_config FROM mcp_servers%s ORDER BY %s`, nameColumn, configColumn, where, order)
	out, err := exec.Command(sqlite, "-readonly", "-json", path, query).Output()
	if err != nil {
		return nil, fmt.Errorf("cc-switch import: read %s: %w", path, err)
	}
	if strings.TrimSpace(string(out)) == "" {
		return nil, nil
	}
	var rows []ccSwitchMCPRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("cc-switch import: parse sqlite output: %w", err)
	}
	return ccSwitchRowsToPlugins(rows)
}

func readSQLiteColumns(sqlite, path, table string) (map[string]bool, error) {
	out, err := exec.Command(sqlite, "-readonly", "-json", path, "PRAGMA table_info("+table+")").Output()
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, err
	}
	columns := map[string]bool{}
	for _, row := range rows {
		columns[row.Name] = true
	}
	return columns, nil
}

func ccSwitchRowsToPlugins(rows []ccSwitchMCPRow) ([]PluginEntry, error) {
	entries := make([]PluginEntry, 0, len(rows))
	for _, row := range rows {
		var s mcpServerSpec
		if err := json.Unmarshal([]byte(row.ServerConfig), &s); err != nil {
			return nil, fmt.Errorf("cc-switch import: server %q config: %w", row.Name, err)
		}
		e := pluginFromMCPServerSpec(row.Name, s)
		markImportedAutoStart(&e)
		if err := validatePlugin(e); err != nil {
			return nil, fmt.Errorf("cc-switch import: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func loadCCSwitchLegacyConfig(path string) ([]PluginEntry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		MCP struct {
			Servers map[string]ccSwitchLegacyServer `json:"servers"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("cc-switch import: parse %s: %w", path, err)
	}
	keys := make([]string, 0, len(doc.MCP.Servers))
	for key := range doc.MCP.Servers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var entries []PluginEntry
	for _, key := range keys {
		srv := doc.MCP.Servers[key]
		if !srv.Apps.Codex {
			continue
		}
		name := srv.Name
		if name == "" {
			name = srv.ID
		}
		if name == "" {
			name = key
		}
		e := pluginFromMCPServerSpec(name, srv.Server)
		markImportedAutoStart(&e)
		if err := validatePlugin(e); err != nil {
			return nil, fmt.Errorf("cc-switch import: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func pluginFromMCPServerSpec(name string, s mcpServerSpec) PluginEntry {
	return PluginEntry{
		Name:             name,
		Type:             s.Type,
		Command:          s.Command,
		Args:             s.Args,
		Env:              s.Env,
		CWD:              s.CWD,
		URL:              s.URL,
		Headers:          s.Headers,
		AutoStart:        s.AutoStart,
		RequestTimeoutMs: s.RequestTimeoutMs,
	}
}

func markImportedAutoStart(e *PluginEntry) {
	if e.AutoStart != nil {
		return
	}
	auto := !looksHeavyImportedMCP(*e)
	e.AutoStart = &auto
}

func looksHeavyImportedMCP(e PluginEntry) bool {
	typ := strings.ToLower(strings.TrimSpace(e.Type))
	if typ == "sse" {
		return true
	}
	name := strings.ToLower(e.Name)
	if strings.Contains(name, "chrome-devtools") {
		return true
	}
	if typ != "" && typ != "stdio" {
		return false
	}
	cmd := strings.ToLower(filepath.Base(e.Command))
	if cmd != "npx" && cmd != "uvx" {
		return false
	}
	for _, a := range e.Args {
		if strings.Contains(strings.ToLower(a), "@latest") {
			return true
		}
	}
	return false
}

func classifyMCPImportCandidate(e PluginEntry) MCPImportCandidate {
	c := MCPImportCandidate{Entry: e, Recommended: e.ShouldAutoStart()}
	typ := strings.ToLower(strings.TrimSpace(e.Type))
	name := strings.ToLower(e.Name)
	cmd := strings.ToLower(filepath.Base(e.Command))
	if c.Recommended {
		c.Reasons = append(c.Reasons, "recommended")
	}
	if !e.ShouldAutoStart() {
		c.Reasons = append(c.Reasons, "manual")
	}
	if typ == "sse" {
		c.Reasons = append(c.Reasons, "unsupported sse")
	}
	if strings.Contains(name, "chrome-devtools") {
		c.Reasons = append(c.Reasons, "browser/heavy")
	}
	if cmd == "npx" || cmd == "uvx" {
		for _, a := range e.Args {
			if strings.Contains(strings.ToLower(a), "@latest") {
				c.Reasons = append(c.Reasons, "@latest")
				break
			}
		}
	}
	if len(e.Headers) > 0 || len(e.Env) > 0 {
		c.Reasons = append(c.Reasons, "auth/env")
		if !isCommonDocMCP(name) {
			c.Recommended = false
		}
	}
	if strings.Contains(name, "flomo") || strings.Contains(name, "dida") ||
		strings.Contains(name, "ynote") || strings.Contains(name, "youdao") {
		c.Reasons = append(c.Reasons, "personal")
		c.Recommended = false
	}
	if len(c.Reasons) == 0 {
		c.Reasons = append(c.Reasons, "candidate")
	}
	return c
}

func isCommonDocMCP(name string) bool {
	return strings.Contains(name, "context7") || strings.Contains(name, "exa") || strings.Contains(name, "fetch")
}
