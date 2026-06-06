package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConfigDiagnostic records issues found during config loading that don't prevent
// startup: malformed optional files, ignored project-level files, migration state.
type ConfigDiagnostic struct {
	MalformedFiles  []string `json:"malformed_files,omitempty"`
	IgnoredFiles    []string `json:"ignored_files,omitempty"`
	MigrationStatus string   `json:"migration_status,omitempty"`
	ImportSkipped   int      `json:"import_skipped,omitempty"`
	ImportImported  int      `json:"import_imported,omitempty"`
	ImportErrors    []string `json:"import_errors,omitempty"`
}

var loadDiags ConfigDiagnostic

func addLoadDiagnostic(file, msg string) {
	loadDiags.MalformedFiles = append(loadDiags.MalformedFiles, fmt.Sprintf("%s: %s", file, msg))
}

// CollectDiagnostics returns the accumulated load-time diagnostics and resets.
func CollectDiagnostics() ConfigDiagnostic {
	d := loadDiags
	loadDiags = ConfigDiagnostic{}
	return d
}

// DetectIgnoredProjectFiles checks the given root directory for project-level
// config files that are now ignored (reasonix.toml, .mcp.json, .reasonix/*,
// .env) and records them in diagnostics.
func DetectIgnoredProjectFiles(root string) {
	root = resolveRoot(root)
	if root == "." {
		if wd, err := os.Getwd(); err == nil {
			root = wd
		}
	}
	// Files we can import via `reasonix config import --from`
	importable := map[string]bool{
		".toml": true,
		".json": true,
	}
	// Top-level files
	for _, name := range []string{"reasonix.toml", ".mcp.json", ".env"} {
		p := filepath.Join(root, name)
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		redacted := redactPath(p)
		if importable[ext] {
			loadDiags.IgnoredFiles = append(loadDiags.IgnoredFiles,
				fmt.Sprintf("%s (file) — use `reasonix config import --from %s` to migrate", redacted, redacted))
		} else {
			loadDiags.IgnoredFiles = append(loadDiags.IgnoredFiles,
				fmt.Sprintf("%s (file) — project-level .env is no longer read; move settings to %s", redacted, redactPath(UserCredentialsPath())))
		}
		_ = info
	}
	// Convention directory entries are skills/commands — not importable one-by-one
	for _, c := range ConventionDirs {
		for _, sub := range []string{"skills", "commands"} {
			p := filepath.Join(root, c, sub)
			if info, err := os.Stat(p); err == nil && info.IsDir() {
				redacted := redactPath(p)
				loadDiags.IgnoredFiles = append(loadDiags.IgnoredFiles,
					fmt.Sprintf("%s (directory) — project-level %s are no longer scanned; move them to ~/.reasonix/%s or use custom paths in skills.toml", redacted, sub, sub))
			}
		}
	}
}

// RenderDiagnostics returns a human-readable summary of load-time diagnostics.
func RenderDiagnostics(d ConfigDiagnostic) string {
	if len(d.MalformedFiles) == 0 && len(d.IgnoredFiles) == 0 && d.MigrationStatus == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("Configuration diagnostics:\n")
	if d.MigrationStatus != "" {
		fmt.Fprintf(&b, "  migration: %s\n", d.MigrationStatus)
	}
	for _, m := range d.MalformedFiles {
		fmt.Fprintf(&b, "  malformed: %s\n", m)
	}
	for _, i := range d.IgnoredFiles {
		fmt.Fprintf(&b, "  ignored: %s\n", i)
	}
	if d.ImportImported > 0 || d.ImportSkipped > 0 {
		fmt.Fprintf(&b, "  import: %d imported, %d skipped\n", d.ImportImported, d.ImportSkipped)
	}
	for _, e := range d.ImportErrors {
		fmt.Fprintf(&b, "  import error: %s\n", e)
	}
	return b.String()
}

func redactPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}
