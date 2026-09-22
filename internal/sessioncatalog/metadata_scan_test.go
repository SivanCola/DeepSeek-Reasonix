package sessioncatalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
)

func TestMetadataDiscoveryNeverRepairsOrClassifiesContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recovery.jsonl")
	body := []byte("deliberately invalid authoritative content\n")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := agent.SaveBranchMeta(path, agent.BranchMeta{Recovered: true, ParentID: "parent", TopicID: "recovered-topic", Scope: "global"}); err != nil {
		t.Fatal(err)
	}
	c, err := Open(t.Context(), Options{InMemory: true, MetadataOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	c.testSessionContentLoadHook = func(string) { t.Error("metadata discovery read content") }
	if !c.opts.DisableRepair {
		t.Fatal("metadata mode admitted background content repair")
	}
	if err := c.ReconcileDirectory(t.Context(), DirectoryTarget{Path: dir, Scope: "global"}); err != nil {
		t.Fatal(err)
	}
	record, found, err := c.GetSession(t.Context(), path)
	if err != nil || !found || !record.OrdinaryVisible || record.TurnsState != TurnsUnknown || record.RecoveryCopy {
		t.Fatalf("unverified recovery must remain reachable: %+v %v %v", record, found, err)
	}
	if err := c.IndexSessionPath(t.Context(), DirectoryTarget{Path: dir, Scope: "global"}, path); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(body) {
		t.Fatal("metadata discovery changed authoritative content")
	}
	if !c.DirectoryScanReady(t.Context(), dir) {
		t.Fatal("unknown content count prevented directory readiness")
	}
}

func TestMetadataDiscoveryOversizedSidecarStillVisible(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.jsonl")
	if err := os.WriteFile(path, []byte("invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agent.BranchMetaPath(path), []byte(strings.Repeat("x", 70<<10)), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Open(t.Context(), Options{InMemory: true, MetadataOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	if err := c.ReconcileDirectory(t.Context(), DirectoryTarget{Path: dir, Scope: "global"}); err != nil {
		t.Fatal(err)
	}
	page, err := c.ListTopics(t.Context(), TopicPageRequest{Scope: "global", Limit: 50})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("metadata fallback disappeared: %+v %v", page, err)
	}
}

func TestMetadataDiscoveryCancellationCannotMarkRowsMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.jsonl")
	if err := os.WriteFile(path, []byte("invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Open(t.Context(), Options{InMemory: true, MetadataOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	target := DirectoryTarget{Path: dir, Scope: "global"}
	if err := c.ReconcileDirectory(t.Context(), target); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.ReconcileDirectory(ctx, target); err == nil {
		t.Fatal("cancelled scan succeeded")
	}
	row, found, err := c.GetSession(t.Context(), path)
	if err != nil || !found || row.MissingSince != 0 {
		t.Fatalf("cancelled scan marked missing: %+v %v", row, err)
	}
}
