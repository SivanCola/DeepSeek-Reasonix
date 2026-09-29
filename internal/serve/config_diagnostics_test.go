package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/servecontract"
)

func TestConfigDiagnosticsUsesBoundWorkspace(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "reasonix.toml"), []byte("[permissions]\nallow=['Bash=echo exact']\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, _, _, ref := newExclusiveSessionServeWithOptions(t, func(o *control.Options) { o.WorkspaceRoot = root })
	if !slices.Contains(s.capabilities(), servecontract.ConfigDiagnosticsV1) {
		t.Fatal("missing capability")
	}
	server := httptest.NewServer(operatorHandler(s))
	defer server.Close()
	resp, err := http.Get(server.URL + "/config-diagnostics?sessionId=" + ref.SessionID + "&root=/must-not-read")
	if err != nil {
		t.Fatal(err)
	}
	var view config.DiagnosticSnapshot
	err = json.NewDecoder(resp.Body).Decode(&view)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || view.WorkspaceRoot != root || view.Status != "ready" || len(view.Items) != 1 || view.Items[0].Status != "on_demand" {
		t.Fatalf("status=%d err=%v view=%+v", resp.StatusCode, err, view)
	}
	response, data := postFixedSessionExport(t, server.URL, "/session-export/diagnostic", ref.SessionID, ref.SessionID,
		map[string]any{"configDiagnostics": "forged-local-config"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("export: %d %s", response.StatusCode, data)
	}
	var exported struct {
		ConfigDiagnostics config.DiagnosticSnapshot `json:"configDiagnostics"`
	}
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatal(err)
	}
	if exported.ConfigDiagnostics.WorkspaceRoot != root || len(exported.ConfigDiagnostics.Items) != 1 || exported.ConfigDiagnostics.Items[0].Values != nil {
		t.Fatalf("wrong exported diagnostics: %s", data)
	}
	resp, err = http.Get(server.URL + "/config-diagnostics?sessionId=missing")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("accepted another binding")
	}
}
