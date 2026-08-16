package main

import (
	"context"
	"os"
	"testing"
	"time"

	"reasonix/internal/sessioncatalog"
)

func waitForCatalogSessionPath(t *testing.T, app *App, workspaceRoot, sessionDir, sessionPath string) sessioncatalog.SessionRecord {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last []sessioncatalog.SessionRecord
	for time.Now().Before(deadline) {
		catalog := app.sessionCatalog.Load()
		if catalog == nil {
			t.Fatal("session catalog is not installed")
		}
		page, err := catalog.ListSessions(context.Background(), sessioncatalog.SessionPageRequest{
			Scope: "project", WorkspaceRoot: workspaceRoot, Directory: sessionDir, Limit: 20,
		})
		if err != nil {
			t.Fatalf("list project sessions: %v", err)
		}
		last = page.Items
		for _, item := range page.Items {
			if item.Path == sessionPath {
				return item
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("project session %q did not become visible: %#v", sessionPath, last)
	return sessioncatalog.SessionRecord{}
}

func TestRegisterProjectRootReconcilesNewProjectSessionDirectory(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	globalDir := desktopSessionDir(globalWorkspaceRoot())
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global session dir: %v", err)
	}
	installSessionCatalogForTest(t, app, globalDir, "global", "")

	root := t.TempDir()
	sessionDir := desktopSessionDir(root)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir project session dir: %v", err)
	}
	sessionPath := writeTopicSession(t, sessionDir, "existing.jsonl", "topic-existing", "Existing topic", root)

	app.registerProjectRoot(root)

	session := waitForCatalogSessionPath(t, app, root, sessionDir, sessionPath)
	if session.TopicTitle != "Existing topic" {
		t.Fatalf("session topic title = %q, want Existing topic", session.TopicTitle)
	}
}
