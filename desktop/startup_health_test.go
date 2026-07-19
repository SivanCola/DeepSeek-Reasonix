package main

import (
	"context"
	"path/filepath"
	"testing"

	"reasonix/internal/control"
	"reasonix/internal/repair"
)

type shutdownSnapshotController struct {
	control.SessionAPI
	calls           []string
	normalSnapshots int
}

func (c *shutdownSnapshotController) Snapshot() error {
	c.normalSnapshots++
	return nil
}

func (c *shutdownSnapshotController) SnapshotForShutdown() error {
	c.calls = append(c.calls, "shutdown-snapshot")
	return nil
}

func (c *shutdownSnapshotController) Close() {
	c.calls = append(c.calls, "close")
	if c.SessionAPI != nil {
		c.SessionAPI.Close()
	}
}

// TestShutdownDoesNotBlessStartupBeforeReady pins the recovery contract that a
// clean exit before the window ever reached domReady keeps the incomplete
// startup record: quitting a build that boots but never paints must not reset
// the crash-loop counter (nor bless a probationary update), or repeated
// attempts would never reach the Guard recovery threshold and the rollback
// backups would be deleted under a broken release.
func TestShutdownDoesNotBlessStartupBeforeReady(t *testing.T) {
	isolateDesktopUserDirs(t)
	tracker := repair.NewStartupTracker(filepath.Join(t.TempDir(), "startup-state.json"))
	if _, err := tracker.Begin("test-version", false); err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	a.startupTracker = tracker

	a.shutdown(context.Background())
	state, err := tracker.Read()
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != "starting" {
		t.Fatalf("pre-ready shutdown must keep the incomplete phase, got %q", state.Phase)
	}

	a.startupReady.Store(true)
	a.shutdown(context.Background())
	state, err = tracker.Read()
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != "clean-exit" {
		t.Fatalf("post-ready shutdown must mark clean-exit, got %q", state.Phase)
	}
}

func TestShutdownUsesDurableSnapshotBeforeClosingController(t *testing.T) {
	isolateDesktopUserDirs(t)
	ctrl := &shutdownSnapshotController{SessionAPI: control.New(control.Options{Label: "shutdown"})}
	a := NewApp()
	a.tabs["tab"] = &WorkspaceTab{ID: "tab", Ctrl: ctrl}
	a.tabOrder = []string{"tab"}

	a.shutdown(context.Background())

	if ctrl.normalSnapshots != 0 {
		t.Fatalf("ordinary Snapshot calls = %d, want shutdown-specific persistence", ctrl.normalSnapshots)
	}
	if len(ctrl.calls) != 2 || ctrl.calls[0] != "shutdown-snapshot" || ctrl.calls[1] != "close" {
		t.Fatalf("shutdown call order = %v, want [shutdown-snapshot close]", ctrl.calls)
	}
}
