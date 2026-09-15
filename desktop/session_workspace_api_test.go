package main

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"reasonix/desktop/internal/workspacestate"
	"reasonix/internal/session"
)

func TestWorkspaceSessionListSurvivesRuntimePruneAndAppRestart(t *testing.T) {
	root := t.TempDir()
	sessionRoot := filepath.Join(root, "desktop-sessions-v5", "by-id")
	statePath := filepath.Join(root, "desktop", "workspace-state-v1.json")
	projectRoot := filepath.Join(root, "project")

	first := NewApp()
	first.desktopSessionRoot = sessionRoot
	first.workspaceState = workspacestate.NewStore(statePath)
	service := first.desktopSessionService(filepath.Join(projectRoot, "sessions"))
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: "durable-session", CWD: projectRoot, Origin: session.SessionOriginNew})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	payload, err := json.Marshal(map[string]any{"message": map[string]any{"id": "user-1", "role": "user", "content": "keep me"}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if _, err := runtime.Session().AppendBatch(t.Context(), "user-turn", []session.Event{{Kind: "message/complete", Payload: payload}}); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	workspaceID, err := first.ensureDesktopWorkspace(t.Context(), "project", projectRoot)
	if err != nil {
		t.Fatalf("ensureDesktopWorkspace: %v", err)
	}
	if err := first.workspaceState.AttachSession(t.Context(), "", workspaceID, runtime.Ref().SessionID, ""); err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	first.closeSessionServices()

	second := NewApp()
	second.desktopSessionRoot = sessionRoot
	second.workspaceState = workspacestate.NewStore(statePath)
	page, err := second.ListWorkspaceSessions(workspaceID, "", "", 10, false)
	if err != nil {
		t.Fatalf("ListWorkspaceSessions after restart: %v", err)
	}
	if len(page.Sessions) != 1 || page.Sessions[0].Ref.SessionID != "durable-session" || page.Sessions[0].Preview != "keep me" {
		t.Fatalf("sessions after restart = %#v", page.Sessions)
	}
	if err := second.ArchiveCanonicalSession(page.Sessions[0].Ref); err != nil {
		t.Fatalf("ArchiveCanonicalSession: %v", err)
	}
	visible, err := second.ListWorkspaceSessions(workspaceID, "", "", 10, false)
	if err != nil {
		t.Fatalf("List visible: %v", err)
	}
	if len(visible.Sessions) != 0 {
		t.Fatalf("visible archived sessions = %#v", visible.Sessions)
	}
	archived, err := second.ListWorkspaceSessions(workspaceID, "", "", 10, true)
	if err != nil {
		t.Fatalf("List archived: %v", err)
	}
	if len(archived.Sessions) != 1 || !archived.Sessions[0].Archived {
		t.Fatalf("archived sessions = %#v", archived.Sessions)
	}
	if err := second.RestoreCanonicalSession(archived.Sessions[0].Ref); err != nil {
		t.Fatalf("RestoreCanonicalSession: %v", err)
	}
}
