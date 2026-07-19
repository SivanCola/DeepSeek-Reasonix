package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/repair"
)

func TestShutdownWaitsForRuntimeLifecycleMutation(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.runtimeAdmissionMu.Lock()
	admissionHeld := true
	defer func() {
		if admissionHeld {
			app.runtimeAdmissionMu.Unlock()
		}
	}()

	done := make(chan struct{})
	go func() {
		app.shutdown(context.Background())
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for app.runtimeRebuildMu.TryLock() {
		app.runtimeRebuildMu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("shutdown did not enter the runtime lifecycle barrier")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-done:
		t.Fatal("shutdown bypassed an in-flight runtime lifecycle mutation")
	default:
	}

	app.runtimeAdmissionMu.Unlock()
	admissionHeld = false
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not resume after the runtime lifecycle mutation completed")
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
