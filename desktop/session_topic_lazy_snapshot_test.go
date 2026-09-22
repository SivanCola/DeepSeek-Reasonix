package main

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/sessioncatalog"
)

func TestMetadataTopicSnapshotReadsPagesWithoutMaterializingHistory(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Large history"); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	catalog, err := sessioncatalog.Open(t.Context(), sessioncatalog.Options{Path: filepath.Join(t.TempDir(), "catalog.sqlite"), MetadataOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	app.sessionCatalog.Store(catalog)
	t.Cleanup(func() { app.desktopSessions.readSnapshots.close(); app.stopSessionCatalog(time.Second) })
	rows := make([]sessioncatalog.SessionRecord, 203)
	for i := range rows {
		// No source files exist: this list can only succeed by reading metadata.
		rows[i] = sessioncatalog.SessionRecord{Path: filepath.Join(root, fmt.Sprintf("old-%04d.jsonl", i)), Directory: root, Scope: "project", WorkspaceRoot: root, TopicID: "shared-topic", TopicTitle: "Shared", CreatedAt: int64(i + 1), LastActivityAt: int64(i + 1), OrdinaryVisible: true, TurnsState: sessioncatalog.TurnsUnknown, Health: sessioncatalog.HealthOK}
		if err := catalog.UpsertSession(t.Context(), rows[i]); err != nil {
			t.Fatal(err)
		}
	}
	req := ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, Limit: 200}
	first, err := app.ListProjectTopics(req)
	if err != nil || len(first.Items) != 200 || first.NextCursor == "" {
		t.Fatalf("first page: %d %q %v", len(first.Items), first.NextCursor, err)
	}
	store := &app.desktopSessions.readSnapshots
	store.mu.Lock()
	snapshot := store.entries[first.SnapshotID].data
	store.mu.Unlock()
	if snapshot.readPage == nil || snapshot.count != 0 || len(snapshot.rows) != 0 || snapshot.db != nil {
		t.Fatal("first page materialized history instead of retaining a catalog view")
	}
	// Concurrent activity changes ordering for a new view only.
	rows[0].LastActivityAt = 10000
	if err := catalog.UpsertSession(context.Background(), rows[0]); err != nil {
		t.Fatal(err)
	}
	req.Cursor = first.NextCursor
	second, err := app.ListProjectTopics(req)
	if err != nil || len(second.Items) != 3 || second.NextCursor != "" || second.Items[2].SessionPath != rows[0].Path {
		t.Fatalf("snapshot continuation: %+v %v", second, err)
	}
	req.Cursor = ""
	fresh, err := app.ListProjectTopics(req)
	if err != nil || fresh.Items[0].SessionPath != rows[0].Path {
		t.Fatalf("refresh: %+v %v", fresh, err)
	}
	app.ReleaseReadSnapshot(first.SnapshotID)
	if !snapshot.released || snapshot.closeRead != nil {
		t.Fatal("released cursor retained the WAL view")
	}
}
