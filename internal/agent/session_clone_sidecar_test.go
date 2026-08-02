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
	for _, tc := range []struct {
		name string
		path func(string) string
	}{
		{name: "checkpoint", path: func(path string) string { return path }},
		{name: "event log", path: store.SessionEventLog},
		{name: "event index", path: store.SessionEventIndex},
		{name: "branch metadata", path: store.SessionMeta},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dst := filepath.Join(dir, tc.name+"-copy.jsonl")
			preExisting := tc.path(dst)
			want := "pre-existing " + tc.name + "\n"
			if err := os.WriteFile(preExisting, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := CloneSessionToPath(src, dst); err == nil {
				t.Fatal("clone over a pre-existing destination artifact must fail")
			}
			b, err := os.ReadFile(preExisting)
			if err != nil {
				t.Fatalf("pre-existing file removed: %s: %v", preExisting, err)
			}
			if string(b) != want {
				t.Fatalf("pre-existing file modified: %q, want %q", b, want)
			}
			for _, path := range []string{dst, store.SessionEventLog(dst), store.SessionEventIndex(dst), store.SessionMeta(dst)} {
				if path == preExisting {
					continue
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("partial clone artifact survived at %s: %v", path, err)
				}
			}
		})
	}
}

func TestSessionCloneDiscardUsesOwnedPaths(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	dir := t.TempDir()
	src := filepath.Join(dir, "source.jsonl")
	if err := (&Session{Messages: []provider.Message{{Role: "user", Content: "hi"}}}).Save(src); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "copy.jsonl")
	clone, err := CloneSessionToPath(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	clone.Discard()
	for _, path := range []string{dst, store.SessionEventLog(dst), store.SessionEventIndex(dst), store.SessionMeta(dst)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("discard left clone-owned artifact %s: %v", path, err)
		}
	}
}
