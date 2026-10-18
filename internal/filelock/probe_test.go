package filelock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExclusiveHeldUsesReadOnlyProbeWithoutCreatingFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lease")
	if held, err := ExclusiveHeld(path); err != nil || held {
		t.Fatalf("missing: held=%v err=%v", held, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("probe created a lock file")
	}
	release, err := Acquire(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })
	if held, err := ExclusiveHeld(path); err != nil || !held {
		t.Fatalf("read-only held lease: held=%v err=%v", held, err)
	}
	release()
	if held, err := ExclusiveHeld(path); err != nil || held {
		t.Fatalf("released: held=%v err=%v", held, err)
	}
}
