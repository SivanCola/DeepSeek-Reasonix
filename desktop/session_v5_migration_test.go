package main

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"reasonix/desktop/internal/workspacestate"
	"reasonix/internal/agent"
	"reasonix/internal/provider"
	"reasonix/internal/session"
)

func TestCanonicalV4MigrationPublishesHeaderThenWorkspaceMembershipIdempotently(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "project", "sessions-v4")
	sourceService, err := session.NewService("source", session.NewFilesystemPersistence(sourceRoot))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := sourceService.Create(t.Context(), session.CreateOptions{SessionID: "legacy-canonical"})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"message": map[string]any{"id": "user", "role": "user", "content": "migrate me"}})
	if _, err := runtime.Session().AppendBatch(t.Context(), "content", []session.Event{{Kind: "message/complete", Payload: payload}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := sourceService.Close(t.Context(), runtime.Ref()); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	app.desktopSessions.root = filepath.Join(root, "desktop-sessions-v5", "by-id")
	app.desktopSessions.workspaceState = workspacestate.NewStore(filepath.Join(root, "desktop", "workspace-state-v1.json"))
	source := desktopMigrationSource{
		root: sourceRoot, scope: "project", workspaceRoot: filepath.Join(root, "workspace"),
		exact: map[string]bool{"legacy-canonical": true},
	}
	if err := app.migrateCanonicalStore(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	if err := app.migrateCanonicalStore(t.Context(), source); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	info, err := app.desktopSessionService("").Query().List(t.Context(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Sessions) != 1 || info.Sessions[0].SessionID != "legacy-canonical" || info.Sessions[0].Origin != session.SessionOriginCanonicalImport {
		t.Fatalf("migrated sessions = %#v", info.Sessions)
	}
	state, err := app.desktopSessions.workspaceState.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := desktopWorkspaceID("project", source.workspaceRoot)
	if got := state.Workspaces[workspaceID].SessionIDs; len(got) != 1 || got[0] != "legacy-canonical" {
		t.Fatalf("workspace sessions = %#v", got)
	}
}

func TestExactLegacyTabMigrationFreezesIntoHeaderBackedSession(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	legacyDir := filepath.Join(root, "sessions")
	legacyPath := filepath.Join(legacyDir, "open-tab.jsonl")
	legacy := agent.NewSession("system")
	legacy.Add(provider.Message{ID: "user", Role: provider.RoleUser, Content: "legacy content"})
	if err := legacy.Save(legacyPath); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	app.desktopSessions.root = filepath.Join(root, "desktop-sessions-v5", "by-id")
	app.desktopSessions.workspaceState = workspacestate.NewStore(filepath.Join(root, "desktop", "workspace-state-v1.json"))
	source := desktopMigrationSource{root: legacyDir, scope: "global", exact: map[string]bool{legacyPath: true}}
	if err := app.migrateLegacyDirectory(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	state, err := app.desktopSessions.workspaceState.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ids := state.Workspaces[workspacestate.GlobalWorkspaceID].SessionIDs
	if len(ids) != 1 {
		t.Fatalf("migrated ids = %#v", ids)
	}
	page, err := app.ReadSessionHistory(session.SessionRef{HostID: "local", SessionID: ids[0]}, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 2 || page.Messages[1].Content != "legacy content" {
		t.Fatalf("legacy history = %#v", page.Messages)
	}
	info, err := app.desktopSessionService("").Query().List(t.Context(), "", 10)
	if err != nil || len(info.Sessions) != 1 || info.Sessions[0].Origin != session.SessionOriginLegacyImport {
		t.Fatalf("legacy header list = %#v, err=%v", info.Sessions, err)
	}
}

func TestPendingCreateRecoveryAttachesDurableSessionAndDropsMissingReservation(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	app.desktopSessions.root = filepath.Join(root, "desktop-sessions-v5", "by-id")
	app.desktopSessions.workspaceState = workspacestate.NewStore(filepath.Join(root, "desktop", "workspace-state-v1.json"))
	workspaceID, err := app.ensureDesktopWorkspace(t.Context(), "project", filepath.Join(root, "project"))
	if err != nil {
		t.Fatal(err)
	}
	for _, pending := range []workspacestate.PendingCreate{
		{OperationID: "durable-op", WorkspaceID: workspaceID, SessionID: "durable"},
		{OperationID: "missing-op", WorkspaceID: workspaceID, SessionID: "missing"},
	} {
		if err := app.desktopSessions.workspaceState.BeginCreate(t.Context(), pending); err != nil {
			t.Fatal(err)
		}
	}
	runtime, err := app.desktopSessionService("").Create(t.Context(), session.CreateOptions{
		SessionID: "durable", CWD: root, Origin: session.SessionOriginNew,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := app.recoverDesktopPendingCreates(t.Context()); err != nil {
		t.Fatal(err)
	}
	state, err := app.desktopSessions.workspaceState.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Workspaces[workspaceID].SessionIDs; len(got) != 1 || got[0] != "durable" {
		t.Fatalf("workspace sessions = %#v", got)
	}
	if len(state.PendingCreates) != 0 {
		t.Fatalf("pending creates = %#v", state.PendingCreates)
	}
}

func TestCanonicalMigrationRemapsConflictingSessionIDDeterministically(t *testing.T) {
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "old")
	sourceService, err := session.NewService("migration-source", session.NewFilesystemPersistence(sourceRoot))
	if err != nil {
		t.Fatal(err)
	}
	sourceRuntime, err := sourceService.Create(t.Context(), session.CreateOptions{SessionID: "same-id"})
	if err != nil {
		t.Fatal(err)
	}
	appendMessage := func(runtime *session.Runtime, id, content string) {
		payload, _ := json.Marshal(map[string]any{"message": map[string]any{"id": id, "role": "user", "content": content}})
		if _, err := runtime.Session().AppendBatch(t.Context(), id, []session.Event{{Kind: "message/complete", Payload: payload}}); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.Session().Flush(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	appendMessage(sourceRuntime, "source", "source content")
	if err := sourceService.Close(t.Context(), sourceRuntime.Ref()); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	app.desktopSessions.root = filepath.Join(root, "desktop-sessions-v5", "by-id")
	app.desktopSessions.workspaceState = workspacestate.NewStore(filepath.Join(root, "desktop", "workspace-state-v1.json"))
	targetRuntime, err := app.desktopSessionService("").Create(t.Context(), session.CreateOptions{SessionID: "same-id", CWD: root, Origin: session.SessionOriginNew})
	if err != nil {
		t.Fatal(err)
	}
	appendMessage(targetRuntime, "target", "different target content")

	source := desktopMigrationSource{root: sourceRoot, scope: "global", exact: map[string]bool{"same-id": true}}
	if err := app.migrateCanonicalStore(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	if err := app.migrateCanonicalStore(t.Context(), source); err != nil {
		t.Fatalf("repeat conflict migration: %v", err)
	}
	state, err := app.desktopSessions.workspaceState.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ids := state.Workspaces[workspacestate.GlobalWorkspaceID].SessionIDs
	if len(ids) != 1 || len(ids[0]) < len("migr-") || ids[0][:len("migr-")] != "migr-" {
		t.Fatalf("conflict ids = %#v", ids)
	}
	page, err := app.ReadSessionHistory(session.SessionRef{HostID: localDesktopHostID, SessionID: ids[0]}, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 1 || page.Messages[0].Content != "source content" {
		t.Fatalf("remapped history = %#v", page.Messages)
	}
}
