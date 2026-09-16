package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/provider"
)

func writeTargetHistoryFixture(t *testing.T, dir, name, marker string) string {
	t.Helper()
	path := filepath.Join(dir, name+".jsonl")
	session := agent.NewSession("")
	for i := 0; i < 4; i++ {
		session.Add(provider.Message{Role: provider.RoleUser, Content: marker + " user"})
		session.Add(provider.Message{Role: provider.RoleAssistant, Content: marker + " answer"})
	}
	if err := session.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	if err := agent.UpdateBranchMeta(path, false, func(meta *agent.BranchMeta) error {
		meta.Scope = "global"
		meta.TopicID = name
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHistorySliceForColdLegacyTargetDoesNotNavigateAndBindsCursor(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	first := writeTargetHistoryFixture(t, dir, "first", "first")
	second := writeTargetHistoryFixture(t, dir, "second", "second")
	app := NewApp()
	installSessionCatalogForTest(t, app, dir, "global", "")
	app.tabs = map[string]*WorkspaceTab{"active": {ID: "active", TopicID: "unrelated", SessionID: "unrelated"}}
	app.activeTabID = "active"

	page, err := app.HistorySliceForTarget(SessionSelector{SessionPath: first}, HistorySliceRequest{Turns: 1, Entries: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) == 0 || !page.HasOlder || page.NextCursor == "" {
		t.Fatalf("first page = %+v, want bounded page with older cursor", page)
	}
	if app.activeTabID != "active" || app.tabs["active"].TopicID != "unrelated" {
		t.Fatal("cold target history changed active navigation")
	}
	if _, err := app.HistorySliceForTarget(SessionSelector{SessionPath: second}, HistorySliceRequest{Cursor: page.NextCursor, Turns: 1, Entries: 2}); err == nil ||
		!strings.Contains(err.Error(), "session_operation:stale_cursor:") {
		t.Fatalf("cross-target cursor = %v, want stale_cursor", err)
	}
}
