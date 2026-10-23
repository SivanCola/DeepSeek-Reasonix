package sessionstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/base/testenv"
	"reasonix/internal/contract/provider"
)

// holdForeignLease makes path look leased by another live runtime, using the
// same idiom as TestSessionLeaseHeldByOtherRuntime: this process holds the OS
// lease lock (as a live holder does for its whole lifetime) but the info file
// names a different writer/PID.
func holdForeignLease(t *testing.T, path string) {
	t.Helper()
	key := canonicalSessionSavePath(path)
	unlock, err := tryLockSessionLeaseFile(key)
	if err != nil {
		t.Fatalf("take lease lock: %v", err)
	}
	t.Cleanup(unlock)
	if err := SaveSessionLeaseInfo(key, SessionLeaseInfo{
		SessionPath: key,
		WriterID:    "other-host-777-live",
		PID:         os.Getpid() + 1,
		AcquiredAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save foreign lease info: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(sessionLeaseInfoPath(key)) })
}

// seedFlatImportLeaves writes the shape a real upgrade leaves behind: a v0.x
// event log from days ago in the legacy dir whose meta names a workspace that
// still exists, plus the native (WAL-backed) transcript a previous flat import
// copied into the global dir with the event log's mtime stamped onto it.
func seedFlatImportLeaves(t *testing.T, src, global string) (flat string, live *Session) {
	t.Helper()
	srcLog := filepath.Join(src, "chat-1.events.jsonl")
	if err := os.WriteFile(srcLog, []byte(legacyEventLog), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(srcLog, old, old); err != nil {
		t.Fatal(err)
	}
	flat = filepath.Join(global, "chat-1.jsonl")
	live = &Session{Messages: []provider.Message{{Role: provider.RoleUser, Content: "live turn one"}}}
	if err := live.Save(flat); err != nil {
		t.Fatalf("seed native transcript: %v", err)
	}
	if err := os.Chtimes(flat, old, old); err != nil {
		t.Fatal(err)
	}
	probe, err := probeSessionEventLog(flat)
	if err != nil || !probe.native || probe.size == 0 {
		t.Fatalf("fixture must reach moveNativeFlatImport: native=%v size=%d err=%v", probe.native, probe.size, err)
	}
	return flat, live
}

// A live runtime holds the lease on the flat-import leftover it has open. The
// migration's re-home pass must not move a path another runtime owns: it takes
// the save locks (which no holder keeps between saves) but never asks the
// session lease. Expected: the leased transcript stays put.
func TestMigrateFlatImportLeavesLeasedTranscriptAlone(t *testing.T) {
	src := testenv.TempDir(t)
	global := testenv.TempDir(t)
	workspace := testenv.TempDir(t)
	projRoot := testenv.TempDir(t)
	router := func(ws string) string { return filepath.Join(projRoot, filepath.Base(ws), "sessions") }
	writeLegacyMeta(t, src, "chat-1", workspace, "legacy title")

	flat, live := seedFlatImportLeaves(t, src, global)
	holdForeignLease(t, flat)
	if !SessionLeaseHeldByOtherRuntime(flat) {
		t.Fatal("fixture must model a live foreign holder")
	}

	n, err := MigrateLegacySessions(src, global, router)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	moved := filepath.Join(projRoot, filepath.Base(workspace), "sessions", "chat-1.jsonl")
	_, flatErr := os.Stat(flat)
	_, movedErr := os.Stat(moved)
	if entries, err := os.ReadDir(global); err == nil {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Logf("global dir right after the move: %v", names)
	}
	if flatErr != nil || movedErr == nil {
		t.Errorf("migration re-homed a transcript whose lease %q is held by another live runtime "+
			"(imported=%d): old path gone=%v, project-dir copy present=%v",
			filepath.Base(flat), n, os.IsNotExist(flatErr), movedErr == nil)
	}
	if n != 0 {
		t.Errorf("deferred move counted as imported (%d): the notice would claim it", n)
	}
	if !SessionLeaseHeldByOtherRuntime(flat) {
		t.Errorf("migration detached the holder's lease sidecars from %q", filepath.Base(flat))
	}

	// What the holder's next save does with the path it still owns.
	live.Add(provider.Message{Role: provider.RoleUser, Content: "live turn two"})
	if err := live.Save(flat); err != nil {
		t.Fatalf("holder's next save: %v", err)
	}
	again, err := LoadSession(flat)
	if err != nil {
		t.Fatalf("reload re-created transcript: %v", err)
	}
	movedSession, movedLoadErr := LoadSession(moved)
	// The fixed state has no project-dir copy at all, so only a copy that is
	// present but unreadable is an error; its message count is then the
	// evidence of a split conversation.
	movedCount := 0
	switch {
	case movedLoadErr == nil:
		movedCount = len(movedSession.Messages)
	case !os.IsNotExist(movedLoadErr):
		t.Errorf("project-dir copy unreadable: %v", movedLoadErr)
	}
	t.Logf("after the holder's save: %s has %d message(s), project copy has %d message(s)",
		filepath.Base(flat), len(again.Messages), movedCount)
	if entries, err := os.ReadDir(global); err == nil {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Logf("global dir after the move: %v", names)
	}
}

// Control: the same fixture whose holder saved once since the import. Its
// mtime no longer matches the v0.x event log, so the move is skipped — the
// only thing standing between a live session and this pass is that ±2s mtime
// coincidence with the legacy file, not ownership.
func TestMigrateFlatImportSparesHolderThatHasWrittenSince(t *testing.T) {
	src := testenv.TempDir(t)
	global := testenv.TempDir(t)
	workspace := testenv.TempDir(t)
	projRoot := testenv.TempDir(t)
	router := func(ws string) string { return filepath.Join(projRoot, filepath.Base(ws), "sessions") }
	writeLegacyMeta(t, src, "chat-1", workspace, "legacy title")

	flat, live := seedFlatImportLeaves(t, src, global)
	holdForeignLease(t, flat)
	live.Add(provider.Message{Role: provider.RoleUser, Content: "a later turn"})
	if err := live.Save(flat); err != nil {
		t.Fatalf("holder save: %v", err)
	}

	if _, err := MigrateLegacySessions(src, global, router); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	kept, err := LoadSession(flat)
	if err != nil {
		t.Fatalf("leased transcript must stay at its path: %v", err)
	}
	if len(kept.Messages) != 2 {
		t.Errorf("leased transcript keeps %d message(s), want the holder's 2", len(kept.Messages))
	}
	// The legacy session is still reconstructed into the project dir as its own
	// file; that separate copy must not carry the live runtime's turns.
	moved := filepath.Join(projRoot, filepath.Base(workspace), "sessions", "chat-1.jsonl")
	if b, err := os.ReadFile(moved); err == nil {
		if strings.Contains(string(b), "a later turn") {
			t.Errorf("project-dir copy carries the live runtime's turns:\n%s", b)
		}
	}
}

// Control: with no runtime holding the path, the same leftover is still
// re-homed exactly as before — the liveness gate must not stall the migration.
func TestMigrateFlatImportStillRehomesUnleasedLeftover(t *testing.T) {
	src := testenv.TempDir(t)
	global := testenv.TempDir(t)
	workspace := testenv.TempDir(t)
	projRoot := testenv.TempDir(t)
	router := func(ws string) string { return filepath.Join(projRoot, filepath.Base(ws), "sessions") }
	writeLegacyMeta(t, src, "chat-1", workspace, "legacy title")

	flat, _ := seedFlatImportLeaves(t, src, global)
	n, err := MigrateLegacySessions(src, global, router)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if n != 1 {
		t.Errorf("unleased leftover imported=%d, want 1", n)
	}
	if _, err := os.Stat(flat); !os.IsNotExist(err) {
		t.Errorf("unleased leftover should be retired from the global dir: %v", err)
	}
	moved := filepath.Join(projRoot, filepath.Base(workspace), "sessions", "chat-1.jsonl")
	migrated, err := LoadSession(moved)
	if err != nil {
		t.Fatalf("load re-homed leftover: %v", err)
	}
	if len(migrated.Messages) != 1 || migrated.Messages[0].Content != "live turn one" {
		t.Errorf("re-homed transcript lost its WAL-backed view: %+v", migrated.Messages)
	}
	// The pass completed, so the one-time marker is stamped and a second run
	// is a no-op.
	if !importMarkerExists(global, legacyImportMarker) {
		t.Errorf("completed pass did not stamp %q", legacyImportMarker)
	}
	if again, err := MigrateLegacySessions(src, global, router); err != nil || again != 0 {
		t.Errorf("second run imported=%d err=%v, want 0 nil", again, err)
	}
}

// Control: a lease held by this very process protects the path too.
// SessionLeaseHeldByOtherRuntime reports false for it, so the local half of the
// gate is the one standing between a same-process runtime and this pass.
func TestMigrateFlatImportLeavesLocallyLeasedTranscriptAlone(t *testing.T) {
	src := testenv.TempDir(t)
	global := testenv.TempDir(t)
	workspace := testenv.TempDir(t)
	projRoot := testenv.TempDir(t)
	router := func(ws string) string { return filepath.Join(projRoot, filepath.Base(ws), "sessions") }
	writeLegacyMeta(t, src, "chat-1", workspace, "legacy title")

	flat, _ := seedFlatImportLeaves(t, src, global)
	lease, err := TryAcquireSessionLease(flat)
	if err != nil {
		t.Fatalf("take local lease: %v", err)
	}
	t.Cleanup(lease.Release)
	if SessionLeaseHeldByOtherRuntime(flat) {
		t.Fatal("fixture must not look foreign; the other-runtime half misses it")
	}
	if !sessionLeaseHeldLocally(flat) {
		t.Fatal("fixture must reserve the path in this process's registry")
	}

	n, err := MigrateLegacySessions(src, global, router)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if n != 0 {
		t.Errorf("locally leased leftover imported=%d, want 0", n)
	}
	if _, err := os.Stat(flat); err != nil {
		t.Fatalf("locally leased transcript must stay at its path: %v", err)
	}
	moved := filepath.Join(projRoot, filepath.Base(workspace), "sessions", "chat-1.jsonl")
	if _, err := os.Stat(moved); !os.IsNotExist(err) {
		t.Errorf("locally leased transcript was re-homed: %v", err)
	}
	// The deferred base must not be hidden behind a marker: a later run has to
	// be able to settle it once the holder is gone.
	if importMarkerExists(global, legacyImportMarker) || importMarkerExists(global, legacyJsonlPassMarker) {
		t.Errorf("a deferred move advanced the one-time marker")
	}

	// Exactly what the required fix buys: release the lease (as closing the
	// runtime does) and the next pass settles the same leftover.
	lease.Release()
	if n, err := MigrateLegacySessions(src, global, router); err != nil || n != 1 {
		t.Fatalf("pass after lease release: imported=%d err=%v, want 1 nil", n, err)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Errorf("released leftover was not settled: %v", err)
	}
}
