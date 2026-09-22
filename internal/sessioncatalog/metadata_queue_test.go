package sessioncatalog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestMetadataQueueRotatesBeforeLargeRootCompletes(t *testing.T) {
	large, small := t.TempDir(), t.TempDir()
	for i := 0; i < 400; i++ {
		if err := os.WriteFile(filepath.Join(large, fmt.Sprintf("%04d.jsonl", i)), []byte("unreadable body"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(small, "only.jsonl"), []byte("unreadable body"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Open(t.Context(), Options{Path: filepath.Join(t.TempDir(), "catalog.sqlite"), MetadataOnly: true, StartPaused: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	_, ok := c.ScheduleReconcile(DirectoryTarget{Path: large, Scope: "global"})
	if !ok {
		t.Fatal("large root rejected")
	}
	done, ok := c.ScheduleReconcile(DirectoryTarget{Path: small, Scope: "global"})
	if !ok {
		t.Fatal("small root rejected")
	}
	c.ResumeDiscovery()
	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("small root did not settle")
	}
	if !c.DirectoryScanReady(t.Context(), small) {
		t.Fatal("small root did not publish")
	}
	if c.DirectoryScanReady(t.Context(), large) {
		t.Fatal("small root waited for a complete large scan")
	}
	count, err := c.CountDirectorySessions(t.Context(), large)
	if err != nil || count == 0 || count >= 400 {
		t.Fatalf("large root progress = %d: %v", count, err)
	}
}

func TestMetadataQueueJournalSurvivesRestartWithoutStartingDiscovery(t *testing.T) {
	root, database := t.TempDir(), filepath.Join(t.TempDir(), "catalog.sqlite")
	path := filepath.Join(root, "kept.jsonl")
	if err := os.WriteFile(path, []byte("body must not be decoded"), 0600); err != nil {
		t.Fatal(err)
	}
	opts := Options{Path: database, MetadataOnly: true, StartPaused: true}
	c, err := Open(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	target := DirectoryTarget{Path: root, Scope: "global"}
	if !c.RequestReconcile(target) {
		t.Fatal("request rejected")
	}
	if err := c.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	c, err = Open(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	var pending int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM catalog_pending_roots`).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("pending=%d: %v", pending, err)
	}
	if _, found, err := c.GetSession(t.Context(), path); err != nil || found {
		t.Fatalf("paused startup scanned source: %v %v", found, err)
	}
	done, ok := c.ScheduleReconcile(target)
	if !ok {
		t.Fatal("resume rejected")
	}
	c.ResumeDiscovery()
	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("resumed root did not settle")
	}
	if _, found, err := c.GetSession(t.Context(), path); err != nil || !found {
		t.Fatalf("source missing: %v %v", found, err)
	}
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM catalog_pending_roots`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("completed journal=%d: %v", pending, err)
	}
}
