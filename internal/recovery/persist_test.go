package recovery

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSaveSnapshotIsAtomicAndOwnerOnly(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	const writers = 24
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			snap := Snapshot{Tasks: map[string]*TaskState{
				"root": {Phase: PhaseDiagnosing, Failure: &FailureEvent{ErrSummary: fmt.Sprintf("failure-%d", i)}},
			}}
			if err := SaveSnapshot(sessionPath, snap); err != nil {
				t.Errorf("SaveSnapshot(%d): %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	snap, err := LoadSnapshot(sessionPath)
	if err != nil {
		t.Fatalf("LoadSnapshot after concurrent writes: %v", err)
	}
	if st := snap.Tasks["root"]; st == nil || st.Failure == nil || st.Failure.ErrSummary == "" {
		t.Fatalf("loaded snapshot = %+v", snap)
	}
	info, err := os.Stat(PathFor(sessionPath))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("recovery state permissions = %o, want 600", got)
	}
}
