package main

import (
	"path/filepath"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/session"
)

func TestNewAppPinsDesktopV5SessionRootBeforeStartup(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	t.Cleanup(app.closeSessionServices)

	legacyDir := filepath.Join(t.TempDir(), "project", "sessions")
	if got, want := app.desktopSessions.root, config.DesktopSessionStoreDir(); !sameDesktopPath(got, want) {
		t.Fatalf("desktop session root = %q, want v5 root %q", got, want)
	}
	if sameDesktopPath(app.desktopSessions.root, desktopSessionRoot(legacyDir)) {
		t.Fatal("desktop App regressed to a legacy-derived canonical root before startup")
	}
	if app.desktopSessionService(legacyDir) == nil {
		t.Fatal("desktop session service is nil")
	}
}

func TestDesktopSessionServiceIsSharedAcrossWorkspaces(t *testing.T) {
	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	app.desktopSessions.root = filepath.Join(t.TempDir(), "desktop-sessions-v5", "by-id")

	first := app.desktopSessionService(filepath.Join(t.TempDir(), "project-a", "sessions"))
	second := app.desktopSessionService(filepath.Join(t.TempDir(), "project-b", "sessions"))
	if first == nil || second == nil {
		t.Fatal("desktop session service is nil")
	}
	if first != second {
		t.Fatal("workspace directories selected different canonical services")
	}

	workspaceRoot := filepath.Join(t.TempDir(), "project-a")
	runtime, err := first.Create(t.Context(), session.CreateOptions{SessionID: "shared-session", CWD: workspaceRoot, Origin: session.SessionOriginNew})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	page, err := second.Query().List(t.Context(), "", 10)
	if err != nil {
		t.Fatalf("List through second workspace: %v", err)
	}
	if len(page.Sessions) != 1 || !sameDesktopPath(page.Sessions[0].CWD, workspaceRoot) {
		t.Fatalf("sessions = %#v", page.Sessions)
	}
}
