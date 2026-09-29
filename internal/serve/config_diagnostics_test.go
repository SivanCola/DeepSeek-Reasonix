package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/servecontract"
	"reasonix/internal/stats"
)

func TestConfigDiagnosticsUsesBoundWorkspace(t *testing.T) {
	// The diagnostic export opens the process-wide usage catalog under this
	// home; Windows cannot remove the temp home while that handle is open.
	closeUsage := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := stats.CloseUsageCatalogs(ctx); err != nil {
			t.Fatalf("close usage catalog: %v", err)
		}
	}
	closeUsage()
	t.Setenv("REASONIX_HOME", t.TempDir())
	t.Cleanup(closeUsage)
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
