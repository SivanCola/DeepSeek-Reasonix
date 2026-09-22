package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/provider"
	"reasonix/internal/session"
)

func TestNativeHistoryReadersSharePreparationUntilLastRelease(t *testing.T) {
	a := historySliceTestApp(t)
	t.Cleanup(a.closeHistoryReaders)
	tab := newColdHistoryTab(t, a)
	_, tab.SessionPath = saveHistorySliceSession(t, tabSessionDir(tab), "shared.jsonl", []provider.Message{historySliceUser(0, "one"), historySliceAssistant(0, "answer")})
	// Hold admission so both readers deterministically join an unfinished job.
	resume, err := a.historyMaintenance.Foreground(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer resume()
	one, err := a.BeginSessionHistoryReadForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	two, err := a.BeginSessionHistoryReadForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := a.historyReader(one.ID)
	second, _ := a.historyReader(two.ID)
	if first.native == nil || first.native != second.native {
		t.Fatal("readers started duplicate preparations")
	}
	a.ReleaseSessionHistoryRead(one.ID)
	if second.native.ctx.Err() != nil {
		t.Fatal("one release canceled shared preparation")
	}
	resume()
	page, err := a.ReadSessionHistorySlice(two.ID, HistorySliceRequest{Turns: 1, Entries: 2})
	if err != nil || page.Status != "ready" {
		t.Fatalf("remaining reader: %+v %v", page, err)
	}
	a.ReleaseSessionHistoryRead(two.ID)
	if second.native.ctx.Err() != context.Canceled {
		t.Fatal("last reader did not cancel job")
	}
	a.historyReaders.workers.Wait()
	if err := second.native.pager.DB.Ping(); err == nil {
		t.Fatal("last release retained SQLite handle")
	}
}

func TestHistoricalDirectoryColdReaderDoesNotStartRuntimeOrImport(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := config.SessionStoreDir()
	const id = "cold-original-store"
	coldV4MigrationFixture(t, root, id)
	path := filepath.Join(root, id)
	before, err := desktopSourceFingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	a.ctx = t.Context()
	t.Cleanup(a.closeSessionServices)
	a.tabs["cold"] = &WorkspaceTab{ID: "cold", SessionPath: path, SessionGeneration: 1}
	handle, err := a.BeginSessionHistoryReadForTab("cold")
	if err != nil {
		t.Fatal(err)
	}
	defer a.ReleaseSessionHistoryRead(handle.ID)
	reader, err := a.historyReader(handle.ID)
	if err != nil {
		t.Fatal(err)
	}
	// TitleMessages waits on the same locator preparation, giving this test a
	// deterministic completion barrier without polling a timer.
	if _, err := reader.query.TitleMessages(reader.ctx, reader.ref, 1); err != nil {
		t.Fatal(err)
	}
	page, err := a.ReadSessionHistoryWindow(handle.ID, session.HistoryWindowRequest{Anchor: "newest", Limit: 10})
	if err != nil || page.Status != "ready" || len(page.Messages) == 0 {
		t.Fatalf("cold page: %+v %v", page, err)
	}
	service, err := a.historicalSessionService(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := service.Runtime(reader.ref); exists || a.tabs["cold"].Ctrl != nil {
		t.Fatal("cold read created execution runtime")
	}
	state, err := a.workspaceRegistry().Load(t.Context())
	if err != nil || len(state.SourceMappings) != 0 || len(state.PendingOperations) != 0 {
		t.Fatalf("cold read imported source: %v", err)
	}
	after, err := desktopSourceFingerprint(path)
	if err != nil || before != after {
		t.Fatal("cold read modified authoritative records")
	}
	if _, err := os.Stat(filepath.Join(config.DesktopSessionStoreDir(), id)); !os.IsNotExist(err) {
		t.Fatalf("cold read created new-format copy: %v", err)
	}
	a.ReleaseSessionHistoryRead(handle.ID)
	page, err = a.ReadSessionHistoryWindow(handle.ID, session.HistoryWindowRequest{Anchor: "newest"})
	if err != nil || page.Status != "stale_cursor" {
		t.Fatalf("released handle: %+v %v", page, err)
	}
}

func TestNativeHistorySourceReplacementRetiresOldGeneration(t *testing.T) {
	a := historySliceTestApp(t)
	t.Cleanup(a.closeHistoryReaders)
	tab := newColdHistoryTab(t, a)
	path := filepath.Join(tabSessionDir(tab), "generation.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{\"role\":\"user\",\"content\":\"old\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tab.SessionPath = path
	one, err := a.BeginSessionHistoryReadForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := a.historyReader(one.ID)
	if page, err := a.ReadSessionHistorySlice(one.ID, HistorySliceRequest{Entries: 2}); err != nil || page.Status != "ready" {
		t.Fatalf("initial page: %+v %v", page, err)
	}
	replacement := filepath.Join(filepath.Dir(path), "replacement")
	if err := os.WriteFile(replacement, []byte("{\"role\":\"user\",\"content\":\"new\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	two, err := a.BeginSessionHistoryReadForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := a.historyReader(two.ID)
	if first.native == second.native || first.native.key == second.native.key {
		t.Fatal("replacement reused the old preparation")
	}
	if page, err := a.ReadSessionHistorySlice(two.ID, HistorySliceRequest{Entries: 2}); err != nil || page.Status != "ready" {
		t.Fatalf("replacement page: %+v %v", page, err)
	}
	select {
	case <-first.native.closed:
	default:
		t.Fatal("new cache published before old SQLite handle closed")
	}
	if page, err := a.ReadSessionHistorySlice(one.ID, HistorySliceRequest{Entries: 2}); err != nil || page.Status != "stale_cursor" {
		t.Fatalf("old page: %+v %v", page, err)
	}
	a.ReleaseSessionHistoryRead(one.ID)
	if second.native.ctx.Err() != nil {
		t.Fatal("late old release canceled the new generation")
	}
	a.ReleaseSessionHistoryRead(two.ID)
}
