package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorRootReportsActualCompatibilityWithoutOverrideClaim(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	root := t.TempDir()
	body := "[sandbox]\nworkspace_root = '.'\n[permissions]\nallow = ['Bash=echo do-not-export-this-command']\n"
	if err := os.WriteFile(filepath.Join(root, "reasonix.toml"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	r := Collect(Options{Version: "test", Root: root})
	if r.Config.SourcePath != redactHome(filepath.Join(root, "reasonix.toml")) || len(r.ConfigDiagnostics) != 1 || r.ConfigDiagnostics[0].Count != 1 {
		t.Fatalf("report=%+v", r)
	}
	text := RenderText(r)
	if strings.Contains(text, "overrides user-level") || !strings.Contains(text, "on-demand approval:") {
		t.Fatal(text)
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "do-not-export-this-command") {
		t.Fatal("exported private command")
	}
}
