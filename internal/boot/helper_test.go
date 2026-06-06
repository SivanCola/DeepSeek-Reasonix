package boot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFileRaw writes body to dir/name, trimming a leading newline so test
// literals can start on the line after the backtick. Parent directories are
// created so callers can write nested paths (e.g. .reasonix/skills/x.md).
func writeFileRaw(dir, name, body string) error {
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(strings.TrimPrefix(body, "\n")), 0o644)
}

// isolatedBootHome sets HOME/XDG vars to temp dirs and returns:
// - reasonixRoot: ~/.reasonix (for config.toml, mcp.toml)
// - projectDir: a temp dir used as the workspace (for REASONIX.md, etc.)
func isolatedBootHome(t *testing.T) (reasonixRoot, projectDir string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	proj := t.TempDir()
	t.Chdir(proj)
	root := filepath.Join(home, ".reasonix")
	os.MkdirAll(root, 0o755)
	return root, proj
}
