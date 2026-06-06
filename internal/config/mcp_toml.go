package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// mcpTOMLTop mirrors the top-level structure of ~/.reasonix/mcp.toml.
type mcpTOMLTop struct {
	Plugins []PluginEntry `toml:"plugins"`
}

// loadMCPTOML reads ~/.reasonix/mcp.toml and returns its plugin entries.
// An absent file returns nil, nil. Malformed entries are nonfatal: they are
// skipped and individual errors are collected.
func loadMCPTOML() ([]PluginEntry, error) {
	path := UserMCPConfigPath()
	if path == "" {
		return nil, nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	var top mcpTOMLTop
	if _, err := toml.DecodeFile(path, &top); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	// Validate entries, skipping bad ones.
	var out []PluginEntry
	var errs []string
	for _, p := range top.Plugins {
		if err := validatePlugin(p); err != nil {
			errs = append(errs, fmt.Sprintf("plugin %q: %v", p.Name, err))
			continue
		}
		out = append(out, p)
	}
	if len(errs) > 0 {
		return out, fmt.Errorf("mcp.toml: %s", joinErrors(errs))
	}
	return out, nil
}

// SaveMCPTOML writes the plugin entries to ~/.reasonix/mcp.toml, creating a
// timestamp backup first.
func SaveMCPTOML(plugins []PluginEntry) error {
	path := UserMCPConfigPath()
	if path == "" {
		return fmt.Errorf("save mcp: cannot resolve mcp config path")
	}
	content := RenderMCPTOML(plugins)
	return writeConfigFile(path, content)
}

// RenderMCPTOML serializes plugin entries as annotated TOML.
func RenderMCPTOML(plugins []PluginEntry) string {
	var b string
	b = "# MCP server configuration.\n"
	b += "# These servers are shared across all Reasonix sessions.\n"
	b += "# ${VAR} / ${VAR:-default} are expanded from the environment.\n\n"
	for _, p := range plugins {
		b += "\n[[plugins]]\n"
		b += fmt.Sprintf("name    = %q\n", p.Name)
		if p.Type != "" {
			b += fmt.Sprintf("type    = %q\n", p.Type)
		}
		if p.Command != "" {
			b += fmt.Sprintf("command = %q\n", p.Command)
		}
		if len(p.Args) > 0 {
			b += fmt.Sprintf("args    = %s\n", renderStringArray(p.Args))
		}
		if p.URL != "" {
			b += fmt.Sprintf("url     = %q\n", p.URL)
		}
		if len(p.Headers) > 0 {
			b += fmt.Sprintf("headers = %s\n", renderStringMap(p.Headers))
		}
		if len(p.Env) > 0 {
			b += fmt.Sprintf("env     = %s\n", renderStringMap(p.Env))
		}
		if p.AutoStart != nil {
			b += fmt.Sprintf("auto_start = %v\n", *p.AutoStart)
		}
		if strings.TrimSpace(p.Tier) != "" {
			b += fmt.Sprintf("tier    = %q\n", p.Tier)
		}
	}
	return b
}

// UpsertMCPPlugin adds or replaces an MCP server and persists to mcp.toml.
func UpsertMCPPlugin(e PluginEntry) error {
	plugins, _ := loadMCPTOML()
	for i := range plugins {
		if plugins[i].Name == e.Name {
			plugins[i] = e
			return SaveMCPTOML(plugins)
		}
	}
	plugins = append(plugins, e)
	return SaveMCPTOML(plugins)
}

// RemoveMCPPlugin deletes a named MCP server from mcp.toml.
func RemoveMCPPlugin(name string) (bool, error) {
	plugins, _ := loadMCPTOML()
	for i := range plugins {
		if plugins[i].Name == name {
			plugins = append(plugins[:i], plugins[i+1:]...)
			return true, SaveMCPTOML(plugins)
		}
	}
	return false, nil
}

// ListMCPPlugins returns all MCP servers from mcp.toml.
func ListMCPPlugins() ([]PluginEntry, error) {
	plugins, err := loadMCPTOML()
	if err != nil {
		return nil, err
	}
	return plugins, nil
}

func joinErrors(errs []string) string {
	if len(errs) == 0 {
		return ""
	}
	s := errs[0]
	for _, e := range errs[1:] {
		s += "; " + e
	}
	return s
}
