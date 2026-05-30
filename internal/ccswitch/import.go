// Package ccswitch imports MCP server configurations from a local cc-switch
// SQLite database into reasonix's config.PluginEntry format.
package ccswitch

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"reasonix/internal/config"
)

// DBDir returns the cc-switch data directory (~/.cc-switch on macOS/Linux).
func DBDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find home directory: %w", err)
	}
	return filepath.Join(home, ".cc-switch"), nil
}

// DBPath returns the full path to the cc-switch SQLite database.
func DBPath() (string, error) {
	dir, err := DBDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cc-switch.db"), nil
}

// openDB opens the cc-switch SQLite database in read-only mode.
func openDB(path string) (*sql.DB, error) {
	uri := fmt.Sprintf("file:%s?mode=ro&_journal=off", path)
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, fmt.Errorf("cannot open cc-switch database: %w", err)
	}
	return db, nil
}

// mcpServerRow holds one row from the mcp_servers table.
type mcpServerRow struct {
	Name   string
	Config string // JSON blob
}

// serverConfigJSON is the parsed server_config JSON blob.
type serverConfigJSON struct {
	Type    string            `json:"type"`    // "stdio", "http", "sse"; empty defaults to stdio
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	CWD     string            `json:"cwd"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// Import reads the cc-switch database and returns MCP server configurations
// as PluginEntry values suitable for upserting into reasonix's config.
// Deduplicated by name.
func Import(dbPath string) ([]config.PluginEntry, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("cc-switch database not found at %s", dbPath)
	}

	db, err := openDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT name, server_config FROM mcp_servers ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("cannot read mcp_servers: %w", err)
	}
	defer rows.Close()

	return parseRows(rows)
}

// parseRows iterates query rows and converts them to PluginEntry values,
// deduplicating by name (first wins as ordered).
func parseRows(rows *sql.Rows) ([]config.PluginEntry, error) {
	seen := map[string]bool{}
	var entries []config.PluginEntry

	for rows.Next() {
		var row mcpServerRow
		if err := rows.Scan(&row.Name, &row.Config); err != nil {
			return nil, fmt.Errorf("cannot scan row: %w", err)
		}
		if seen[row.Name] {
			continue
		}
		seen[row.Name] = true

		entry, err := toPluginEntry(row.Name, row.Config)
		if err != nil {
			continue // skip unparseable entries
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// toPluginEntry maps a cc-switch server name + config JSON to a PluginEntry.
func toPluginEntry(name string, raw string) (config.PluginEntry, error) {
	var sc serverConfigJSON
	if err := json.Unmarshal([]byte(raw), &sc); err != nil {
		return config.PluginEntry{}, fmt.Errorf("invalid server_config JSON for %s: %w", name, err)
	}

	entry := config.PluginEntry{Name: name}

	switch sc.Type {
	case "http", "streamable-http":
		entry.Type = "http"
		entry.URL = sc.URL
		if len(sc.Headers) > 0 {
			entry.Headers = sc.Headers
		}
	case "sse":
		entry.Type = "sse"
		entry.URL = sc.URL
		if len(sc.Headers) > 0 {
			entry.Headers = sc.Headers
		}
	default:
		// stdio (empty or "stdio")
		entry.Type = "stdio"
		entry.Command = sc.Command
		if len(sc.Args) > 0 {
			entry.Args = sc.Args
		}
		if len(sc.Env) > 0 {
			entry.Env = sc.Env
		}
	}
	return entry, nil
}
