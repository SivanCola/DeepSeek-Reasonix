package agent

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// TestCloneRefusesPreExistingSidecars verifies the ownership contract: a
// pre-existing authoritative event log, event index or branch metadata at the
// destination is refused and its bytes stay untouched — the clone never
// deletes data it did not create.
func TestCloneRefusesPreExistingSidecars(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	dir := t.TempDir()
	src := filepath.Join(dir, "source.jsonl")
	if err := (&Session{Messages: []provider.Message{{Role: "user", Content: "hi"}}}).Save(src); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "copy.jsonl")
	if err := os.WriteFile(dst, []byte("[]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sentinels := []struct {
		path string
		want string
	}{
		{store.SessionEventLog(dst), "pre-existing authoritative log\n"},
		{store.SessionEventIndex(dst), "pre-existing index\n"},
		{store.SessionMeta(dst), "pre-existing meta\n"},
	}
	for _, sentinel := range sentinels {
		if err := os.MkdirAll(filepath.Dir(sentinel.path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sentinel.path, []byte(sentinel.want), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// The event-log reservation fails (file exists) before any save happens.
	if err := CloneSessionToPath(src, dst); err == nil {
		t.Fatal("clone over pre-existing sidecars must fail")
	}
	// The pre-existing checkpoint and every sidecar stay byte-identical.
	checks := []struct {
		path string
		want string
	}{
		{dst, "[]\n"},
		{store.SessionEventLog(dst), "pre-existing authoritative log\n"},
		{store.SessionEventIndex(dst), "pre-existing index\n"},
		{store.SessionMeta(dst), "pre-existing meta\n"},
	}
	for _, check := range checks {
		b, err := os.ReadFile(check.path)
		if err != nil {
			t.Fatalf("pre-existing file removed: %s: %v", check.path, err)
		}
		if string(b) != check.want {
			t.Errorf("%s modified by failed clone: %q, want %q", check.path, b, check.want)
		}
	}
}
