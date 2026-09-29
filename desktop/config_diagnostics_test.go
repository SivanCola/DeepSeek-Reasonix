package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/config"
)

func TestConfigDiagnosticsSeparatesProjectsAndPublishesEmpty(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	aRoot, bRoot := t.TempDir(), t.TempDir()
	path := filepath.Join(aRoot, "reasonix.toml")
	if err := os.WriteFile(path, []byte("[broken"), 0600); err != nil {
		t.Fatal(err)
	}
	a := &App{ctx: context.Background(), tabs: map[string]*WorkspaceTab{
		"a": {ID: "a", WorkspaceRoot: aRoot}, "b": {ID: "b", WorkspaceRoot: bRoot},
	}}
	first := a.ConfigDiagnostics("a")
	other := a.ConfigDiagnostics("b")
	if len(first.Items) != 1 || other.Status != "ready" || len(other.Items) != 0 || first.WorkspaceID == other.WorkspaceID {
		t.Fatalf("a=%+v b=%+v", first, other)
	}
	if err := os.WriteFile(path, []byte("# repaired\n"), 0600); err != nil {
		t.Fatal(err)
	}
	clear := a.ConfigDiagnostics("a")
	raw, _ := json.Marshal(clear)
	if clear.Items == nil || len(clear.Items) != 0 || clear.Revision <= first.Revision {
		t.Fatalf("clear=%s", raw)
	}
	delete(a.tabs, "a")
	if a.ConfigDiagnostics("a").Status != "unavailable" {
		t.Fatal("closed tab reused diagnostics")
	}
}

func TestRemoteConfigDiagnosticsRejectsReboundResponse(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	a, tab := remoteRuntimeTestApp(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		close(started)
		<-release
		view := config.NewDiagnosticSnapshot("remote", "/project-a", "ready")
		return remoteRuntimeTestResponse(req, http.StatusOK, remoteRuntimeTestJSON(t, view)), nil
	})})
	tab.capabilities["config-diagnostics-v1"] = true
	tab.routing.currentPath = remoteSessionIDRoutePrefix + "source-a"
	done := make(chan config.DiagnosticSnapshot, 1)
	go func() { done <- a.ConfigDiagnostics(tab.id) }()
	<-started
	a.remoteTabMu.Lock()
	tab.routing.currentPath = remoteSessionIDRoutePrefix + "source-b"
	tab.gen++
	a.remoteTabMu.Unlock()
	close(release)
	if view := <-done; view.Status != "unavailable" || len(view.Items) != 0 {
		t.Fatalf("old remote response accepted: %+v", view)
	}
}

func TestRemoteConfigDiagnosticsUnsupportedNeverReadsLocal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[broken"), 0600); err != nil {
		t.Fatal(err)
	}
	a := &App{ctx: context.Background(), remoteTabs: map[string]*remoteTab{"remote": {ref: RemoteTabRef{HostID: "other-host", Workspace: "/remote/workspace"}}}}
	view := a.ConfigDiagnostics("remote")
	if view.Status != "unsupported" || view.HostID != "other-host" || len(view.Items) != 0 {
		t.Fatalf("view=%+v", view)
	}
}
