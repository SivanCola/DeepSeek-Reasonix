package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandDirsIncludeConventions(t *testing.T) {
	dirs := CommandDirs()
	joined := strings.Join(dirs, "\n")
	for _, want := range []string{
		filepath.Join(".claude", "commands"),
		filepath.Join(".agents", "commands"),
		filepath.Join(".agent", "commands"),
		filepath.Join(".reasonix", "commands"),
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("CommandDirs missing %q\ngot:\n%s", want, joined)
		}
	}
	// Home .reasonix/commands or legacy XDG commands must be last.
	// On macOS the XDG path is ~/Library/Application Support/reasonix/commands.
	last := dirs[len(dirs)-1]
	if !strings.HasSuffix(last, filepath.Join(".reasonix", "commands")) &&
		!strings.Contains(last, filepath.Join("reasonix", "commands")) {
		t.Errorf("last entry should be a reasonix commands dir, got %q", last)
	}
}
