package main

import (
	"path/filepath"
	"testing"
)

func TestHistoricalUnavailableHeadHiddenByBothSidebarAdapters(t *testing.T) {
	isolateDesktopUserDirs(t)
	path, _, head := migrationSingleDAGFixture(t)
	app := newHistoricalLifecycleApp(t)
	installSessionCatalogForTest(t, app, filepath.Dir(path), "global", "")
	key := desktopSourceKey(path, head)
	c := &app.historicalImports
	c.mu.Lock()
	c.initialize(t.Context())
	c.sources[key] = historicalSource{path: path, head: head, format: "legacy", scope: "global"}
	c.unavailableSources[key] = true
	c.mu.Unlock()
	req := ProjectTopicPageRequest{Scope: "global", Limit: 50}
	page, err := app.ListProjectTopics(req)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("lazy adapter advertised unavailable head: %+v %v", page.Items, err)
	}
	page, err = app.unadoptedLegacyTopics(req, map[string]bool{}, map[string]bool{})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("materialized adapter advertised unavailable head: %+v %v", page.Items, err)
	}
}
