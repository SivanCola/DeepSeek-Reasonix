package goal

import (
	"os"
	"path/filepath"
)

// DetectTestCommand finds the project's test command by scanning for known build files.
func DetectTestCommand(workspace string) string {
	checks := []struct{ file, cmd string }{
		{"go.mod", "go test ./..."},
		{"package.json", "npm test"},
		{"Cargo.toml", "cargo test"},
		{"Makefile", "make test"},
		{"pyproject.toml", "pytest"},
	}
	for _, c := range checks {
		if _, err := os.Stat(filepath.Join(workspace, c.file)); err == nil {
			return c.cmd
		}
	}
	return ""
}
