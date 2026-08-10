package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func lifecycleTrackerForTest(t *testing.T, root string, pid int, runID string) *desktopLifecycleTracker {
	t.Helper()
	tracker := newDesktopLifecycleTracker(root, "v1.23.0", "stable")
	tracker.state.PID = pid
	tracker.state.RunID = runID
	tracker.path = filepath.Join(tracker.dir, runID+".json")
	tracker.processAlive = func(int) bool { return false }
	return tracker
}

func TestDesktopLifecycleDeadRecordIsConsumedOnce(t *testing.T) {
	root := t.TempDir()
	dead := lifecycleTrackerForTest(t, root, 4242, "dead")
	if err := dead.start(); err != nil {
		t.Fatal(err)
	}
	dead.mark("healthy")

	reader := lifecycleTrackerForTest(t, root, os.Getpid(), "reader")
	got := reader.consumePrevious(true)
	if len(got) != 1 || got[0].Phase != "healthy" || got[0].Version != "v1.23.0" {
		t.Fatalf("observations = %+v", got)
	}
	if replay := reader.consumePrevious(true); len(replay) != 0 {
		t.Fatalf("lifecycle record replayed: %+v", replay)
	}
}

func TestDesktopLifecycleLiveRecordIsPreserved(t *testing.T) {
	root := t.TempDir()
	live := lifecycleTrackerForTest(t, root, 4242, "live")
	if err := live.start(); err != nil {
		t.Fatal(err)
	}

	reader := lifecycleTrackerForTest(t, root, os.Getpid(), "reader")
	reader.processAlive = func(pid int) bool { return pid == 4242 }
	if got := reader.consumePrevious(true); len(got) != 0 {
		t.Fatalf("live record observed: %+v", got)
	}
	if _, err := os.Stat(live.path); err != nil {
		t.Fatalf("live record was removed: %v", err)
	}
}

func TestDesktopLifecycleOptOutConsumesWithoutReporting(t *testing.T) {
	root := t.TempDir()
	dead := lifecycleTrackerForTest(t, root, 4242, "dead")
	if err := dead.start(); err != nil {
		t.Fatal(err)
	}

	reader := lifecycleTrackerForTest(t, root, os.Getpid(), "reader")
	if got := reader.consumePrevious(false); len(got) != 0 {
		t.Fatalf("opt-out returned observations: %+v", got)
	}
	if _, err := os.Stat(dead.path); !os.IsNotExist(err) {
		t.Fatalf("opt-out did not consume dead record: %v", err)
	}
}

func TestDesktopLifecycleCleanRemovesCurrentRecord(t *testing.T) {
	tracker := lifecycleTrackerForTest(t, t.TempDir(), os.Getpid(), "current")
	base := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	tracker.now = func() time.Time { return base }
	if err := tracker.start(); err != nil {
		t.Fatal(err)
	}
	tracker.mark("shutting_down")
	state, err := readDesktopLifecycleState(tracker.path)
	if err != nil || state.Phase != "shutting_down" || state.UpdatedAt != base.Format(time.RFC3339Nano) {
		t.Fatalf("state = %+v err=%v", state, err)
	}
	tracker.clean()
	if _, err := os.Stat(tracker.path); !os.IsNotExist(err) {
		t.Fatalf("clean lifecycle record remains: %v", err)
	}
}
