package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// legacyConfig is the subset of the v0.x (~/.reasonix/config.json) schema this
// import carries forward.
type legacyConfig struct {
	APIKey      string                       `json:"apiKey"`
	BaseURL     string                       `json:"baseUrl"`
	Lang        string                       `json:"lang"`
	MCPServers  map[string]legacyMCPServer   `json:"mcpServers"`
	MCPEnv      map[string]map[string]string `json:"mcpEnv"`
	MCPDisabled []string                     `json:"mcpDisabled"`
}

type legacyMCPServer struct {
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	Transport string            `json:"transport"`
	Type      string            `json:"type"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	Disabled  bool              `json:"disabled"`
}

// MigrationResult summarizes a one-time legacy import for the boot-time notice.
type MigrationResult struct {
	From        string
	To          string
	KeyToEnv    bool
	Plugins     int
	Warnings    []string
	SourceFiles []string
}

func (r *MigrationResult) Notice() string {
	var b strings.Builder
	fmt.Fprintf(&b, "migrated your previous configuration to %s", r.To)
	if r.Plugins > 0 {
		fmt.Fprintf(&b, " (%d MCP server(s))", r.Plugins)
	}
	if r.KeyToEnv {
		b.WriteString("; API key saved to credentials")
	}
	b.WriteString(". The old files were left untouched.")
	for _, w := range r.Warnings {
		b.WriteString("\n  note: " + w)
	}
	return b.String()
}

// MigrateLegacyIfNeeded performs a one-time, non-destructive import of older
// installs into the new ~/.reasonix layout. Checks for existing config first,
// then migrates from old XDG/macOS Library/Windows AppData directories.
func MigrateLegacyIfNeeded() (*MigrationResult, error) {
	dest := UserConfigPath()
	if dest == "" {
		return nil, nil
	}
	// Already migrated: ~/.reasonix/config.toml exists.
	if _, err := os.Stat(dest); err == nil {
		return nil, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}
	// Check old XDG / platform-specific config directories for an existing config.
	if res, err := migrateOldConfigToNew(dest, home); res != nil || err != nil {
		return res, err
	}
	// v0.x ~/.reasonix/config.json
	src := filepath.Join(home, ".reasonix", "config.json")
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, nil
	}
	var legacy legacyConfig
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, fmt.Errorf("parse legacy config %s: %w", src, err)
	}

	cfg := Default()
	res := &MigrationResult{From: src, To: dest}
	if legacy.Lang != "" {
		cfg.Language = legacy.Lang
		_ = cfg.SetDesktopLanguage(legacy.Lang)
	}
	migrateLegacyBaseURL(cfg, legacy.BaseURL)
	mcpPlugins := legacyPlugins(legacy)
	res.Plugins = len(mcpPlugins)

	var envLines []string
	if key := strings.TrimSpace(legacy.APIKey); key != "" {
		envLines = append(envLines, "DEEPSEEK_API_KEY="+key)
		res.KeyToEnv = true
		if base := strings.TrimSpace(legacy.BaseURL); base != "" && !strings.Contains(base, "deepseek.com") {
			res.Warnings = append(res.Warnings, "your previous base_url was "+base+
				" — it was applied to the built-in DeepSeek providers; verify models if this endpoint is not DeepSeek-compatible")
		}
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	if err := cfg.WriteFile(dest); err != nil {
		return nil, fmt.Errorf("write %s: %w", dest, err)
	}
	// Write MCP to separate mcp.toml
	if len(mcpPlugins) > 0 {
		if err := SaveMCPTOML(mcpPlugins); err != nil {
			res.Warnings = append(res.Warnings, "could not write mcp.toml: "+err.Error())
		}
	}
	if len(envLines) > 0 {
		if err := writeCredentialsEnv(home, envLines); err != nil {
			return res, fmt.Errorf("write credentials: %w", err)
		}
	}
	// Copy sessions, archive, memory from old ~/.reasonix to new root
	migrateDataDirs(filepath.Join(home, ".reasonix"), UserRootDir(), res)
	writeMigrationRecord(res)
	return res, nil
}

// migrateOldConfigToNew checks old platform-specific config directories and migrates
// the config to the new ~/.reasonix layout, splitting into config/mcp/skills.
func migrateOldConfigToNew(dest, home string) (*MigrationResult, error) {
	for _, oldRoot := range oldLegacyPaths() {
		src := filepath.Join(oldRoot, "config.toml")
		if _, err := os.Stat(src); err != nil {
			// Also check the old filename: reasonix.toml in the same dirs
			src = filepath.Join(oldRoot, "reasonix.toml")
			if _, err := os.Stat(src); err != nil {
				continue
			}
		}
		cfg := Default()
		if err := mergeFile(cfg, src); err != nil {
			return nil, fmt.Errorf("parse legacy config %s: %w", src, err)
		}
		cfg.ConfigVersion = Default().ConfigVersion
		if strings.TrimSpace(cfg.Desktop.CloseBehavior) == "" && strings.TrimSpace(cfg.UI.CloseBehavior) != "" {
			cfg.Desktop.CloseBehavior = cfg.DesktopCloseBehavior()
		}

		// Split: core stays in config.toml, plugins → mcp.toml, skills → skills.toml
		mcpPlugins := cfg.Plugins
		cfg.Plugins = nil // remove from core config

		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, fmt.Errorf("create config dir: %w", err)
		}
		if err := cfg.WriteFile(dest); err != nil {
			return nil, fmt.Errorf("write %s: %w", dest, err)
		}
		if len(mcpPlugins) > 0 {
			SaveMCPTOML(mcpPlugins)
		}
		SaveSkillsTOML(cfg)

		// Copy data dirs
		migrateDataDirs(oldRoot, UserRootDir(), nil)
		writeMigrationRecord(&MigrationResult{From: src, To: dest, Plugins: len(mcpPlugins)})
		return &MigrationResult{From: src, To: dest, Plugins: len(mcpPlugins)}, nil
	}
	return nil, nil
}

// migrateDataDirs copies sessions, archive, and memory from oldRoot to newRoot
// if the old data exists and the new does not.
func migrateDataDirs(oldRoot, newRoot string, res *MigrationResult) {
	if oldRoot == "" || newRoot == "" || filepath.Clean(oldRoot) == filepath.Clean(newRoot) {
		return
	}
	subdirs := []string{"sessions", "archive", "memory"}
	for _, sub := range subdirs {
		oldDir := filepath.Join(oldRoot, sub)
		newDir := filepath.Join(newRoot, sub)
		if _, err := os.Stat(oldDir); os.IsNotExist(err) {
			continue
		}
		if _, err := os.Stat(newDir); err == nil {
			continue // already has data
		}
		if err := copyDir(oldDir, newDir); err != nil {
			if res != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf("could not copy %s: %v", sub, err))
			}
		}
	}
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			continue
		}
		os.WriteFile(dstPath, data, 0o644)
	}
	return nil
}

// writeMigrationRecord writes a migration.json to ~/.reasonix recording the event.
func writeMigrationRecord(r *MigrationResult) {
	if r == nil {
		return
	}
	record := map[string]any{
		"from":    r.From,
		"to":      r.To,
		"time":    time.Now().UTC().Format(time.RFC3339),
		"plugins": r.Plugins,
	}
	if len(r.Warnings) > 0 {
		record["warnings"] = r.Warnings
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return
	}
	path := MigrationPath()
	if path != "" {
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, append(data, '\n'), 0o644)
	}
}

// ImportConfigFromPath imports a legacy config file (TOML or .mcp.json) into the
// global config. Returns counts and errors.
func ImportConfigFromPath(srcPath string) (imported, skipped int, errors []string) {
	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		errors = append(errors, fmt.Sprintf("file not found: %s", srcPath))
		return
	}
	ext := strings.ToLower(filepath.Ext(srcPath))
	switch ext {
	case ".toml":
		return importTOMLSource(srcPath)
	case ".json":
		return importJSONSource(srcPath)
	default:
		errors = append(errors, fmt.Sprintf("unsupported file type: %s (expected .toml or .json)", srcPath))
	}
	return
}

func importTOMLSource(srcPath string) (imported, skipped int, errors []string) {
	src := Default()
	if err := mergeFile(src, srcPath); err != nil {
		errors = append(errors, err.Error())
		return
	}
	// MCP: skip name conflicts — existing global entry wins.
	existing, _ := loadMCPTOML()
	have := map[string]bool{}
	for _, p := range existing {
		have[p.Name] = true
	}
	for _, p := range src.Plugins {
		if have[p.Name] {
			skipped++
			continue
		}
		if err := UpsertMCPPlugin(p); err != nil {
			errors = append(errors, fmt.Sprintf("plugin %q: %v", p.Name, err))
			skipped++
		} else {
			have[p.Name] = true
			imported++
		}
	}
	// Skills: merge into existing skills.toml content.
	current, _ := loadExistingSkillsForImport()
	for _, p := range src.Skills.Paths {
		current.AddSkillPath(p)
	}
	for _, name := range src.Skills.DisabledSkills {
		current.SetSkillEnabled(name, false)
	}
	SaveSkillsTOML(current)
	return
}

func loadExistingSkillsForImport() (*Config, error) {
	cfg := Default()
	loadSkillsTOML(cfg)
	return cfg, nil
}

func importJSONSource(srcPath string) (imported, skipped int, errors []string) {
	entries, err := loadMCPJSON(srcPath)
	if err != nil {
		errors = append(errors, err.Error())
		return
	}
	// Check for existing names — skip conflicts, don't overwrite.
	existing, _ := loadMCPTOML()
	have := map[string]bool{}
	for _, p := range existing {
		have[p.Name] = true
	}
	for _, e := range entries {
		if have[e.Name] {
			skipped++
			continue
		}
		if err := UpsertMCPPlugin(e); err != nil {
			errors = append(errors, fmt.Sprintf("plugin %q: %v", e.Name, err))
			skipped++
		} else {
			have[e.Name] = true
			imported++
		}
	}
	return
}

func legacyTOMLPaths(dest, home string) []string {
	paths := []string{filepath.Join(filepath.Dir(dest), "reasonix.toml")}
	if home != "" {
		paths = append(paths, filepath.Join(home, ".reasonix", "reasonix.toml"))
	}
	return paths
}

func migrateLegacyBaseURL(cfg *Config, baseURL string) {
	baseURL = strings.TrimSpace(baseURL)
	if cfg == nil || baseURL == "" {
		return
	}
	for i := range cfg.Providers {
		if cfg.Providers[i].APIKeyEnv == "DEEPSEEK_API_KEY" {
			cfg.Providers[i].BaseURL = baseURL
		}
	}
}

func legacyPlugins(legacy legacyConfig) []PluginEntry {
	if len(legacy.MCPServers) == 0 {
		return nil
	}
	disabled := make(map[string]bool, len(legacy.MCPDisabled))
	for _, n := range legacy.MCPDisabled {
		disabled[n] = true
	}
	names := make([]string, 0, len(legacy.MCPServers))
	for n := range legacy.MCPServers {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]PluginEntry, 0, len(names))
	for _, name := range names {
		s := legacy.MCPServers[name]
		pe := PluginEntry{
			Name:    name,
			Type:    normalizeTransport(firstNonEmpty(s.Type, s.Transport)),
			Command: s.Command,
			Args:    s.Args,
			Env:     mergeEnv(s.Env, legacy.MCPEnv[name]),
			URL:     s.URL,
			Headers: s.Headers,
		}
		if s.Disabled || disabled[name] {
			off := false
			pe.AutoStart = &off
		}
		out = append(out, pe)
	}
	return out
}

func normalizeTransport(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "http", "streamable-http":
		return "http"
	case "sse":
		return "sse"
	default:
		return ""
	}
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func mergeEnv(base, overlay map[string]string) map[string]string {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

func writeCredentialsEnv(home string, lines []string) error {
	path := UserCredentialsPath()
	if path == "" {
		path = filepath.Join(home, ".env")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	target := make(map[string]bool, len(lines))
	for _, l := range lines {
		if k, _, ok := strings.Cut(l, "="); ok {
			target[strings.TrimSpace(k)] = true
		}
	}
	var kept []string
	if data, err := os.ReadFile(path); err == nil {
		for _, raw := range strings.Split(string(data), "\n") {
			check := strings.TrimPrefix(strings.TrimSpace(raw), "export ")
			if k, _, ok := strings.Cut(check, "="); ok && target[strings.TrimSpace(k)] {
				continue
			}
			kept = append(kept, raw)
		}
		if n := len(kept); n > 0 && kept[n-1] == "" {
			kept = kept[:n-1]
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	var b strings.Builder
	for _, l := range kept {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
		if k, v, ok := strings.Cut(l, "="); ok {
			os.Setenv(strings.TrimSpace(k), v)
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}
