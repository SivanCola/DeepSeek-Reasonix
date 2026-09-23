package main

import (
	"path/filepath"
	"testing"

	"reasonix/desktop/internal/workspacestate"
	"reasonix/internal/config"
)

func TestArchiveColdHistoricalSessionFromSidebar(t *testing.T) {
	for _, scope := range []string{"global", "project"} {
		t.Run(scope, func(t *testing.T) {
			isolateDesktopUserDirs(t)
			root := config.SessionStoreDir()
			workspace := ""
			if scope == "project" {
				workspace = t.TempDir()
				root = config.ProjectSessionStoreDir(workspace)
				if err := saveProjectsFile(desktopProjectFile{Projects: []desktopProject{{Root: workspace}}}); err != nil {
					t.Fatal(err)
				}
			}
			const id = "cold-history-to-archive"
			coldV4MigrationFixture(t, root, id)
			before := migrationSourceSnapshot(t, canonicalMigrationSourceFiles(root, id))
			app := newHistoricalLifecycleApp(t)
			if _, err := app.ListHistoricalSessions(); err != nil {
				t.Fatal(err)
			}
			page, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: scope, WorkspaceRoot: workspace, Limit: 50})
			if err != nil || len(page.Items) != 1 || page.Items[0].Source == nil {
				t.Fatalf("cold sidebar source: %+v, %v", page.Items, err)
			}
			row := page.Items[0]
			result, err := app.ArchiveSessionTarget(SessionSelector{Source: row.Source, SessionPath: row.SessionPath})
			if err != nil || !result.Committed {
				t.Fatalf("archive cold sidebar row: %+v, %v", result, err)
			}
			state, err := app.workspaceRegistry().Load(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			mapping, exists, err := state.ResolveSource(desktopSourceKey(filepath.Join(root, id), ""))
			if err != nil || !exists || state.SessionStates[mapping.SessionID].Lifecycle != workspacestate.Archived {
				t.Fatalf("archive did not retain the canonical lifecycle: %+v, %v", mapping, err)
			}
			assertMigrationSourceSnapshot(t, before)
		})
	}
}

func TestArchiveHistoricalSessionWithPreviousIdentityAfterRestart(t *testing.T) {
	isolateDesktopUserDirs(t)
	const id = "Previously-Adopted"
	root := config.SessionStoreDir()
	coldV4MigrationFixture(t, root, id)
	app := newHistoricalLifecycleApp(t)
	key := historicalLifecycleID(t, app, id)
	imported, err := app.ImportHistoricalSession(key)
	if err != nil {
		t.Fatal(err)
	}
	app.stopHistoricalImports()
	app.closeSessionServices()
	rewriteHistoricalSourceIdentityForTest(t, app, key)
	app = newHistoricalLifecycleApp(t)
	result, err := app.ArchiveSessionTarget(SessionSelector{Source: &SessionSourceRef{
		HostID: localDesktopHostID, Path: filepath.Join(root, id), SourceKey: key,
	}})
	if err != nil || !result.Committed {
		t.Fatalf("archive previously adopted source after restart: %+v, %v", result, err)
	}
	state, err := app.workspaceRegistry().Load(t.Context())
	if err != nil || state.SessionStates[imported.Session.SessionID].Lifecycle != workspacestate.Archived {
		t.Fatalf("original session was not archived: %v", err)
	}
}
