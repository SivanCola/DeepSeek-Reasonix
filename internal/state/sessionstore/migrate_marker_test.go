package sessionstore

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/base/testenv"
)

// markerSource lays out the dirs a marker test needs. blocked is a regular
// file: routing a session under it makes os.MkdirAll fail with ENOTDIR, which
// keeps the copy failure portable (a chmod would not fail as root).
func markerSource(t *testing.T) (root, src, global, workspace, blocked string) {
	t.Helper()
	root = testenv.TempDir(t)
	src = filepath.Join(root, "src")
	global = filepath.Join(root, "global")
	workspace = filepath.Join(root, "workspace")
	blocked = filepath.Join(root, "blocked")
	for _, dir := range []string{src, global, workspace} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, src, global, workspace, blocked
}

func writeSourceFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The one-time import marker is the completion record for the whole source. A
// project-subdirectory copy that fails must withhold it exactly like a Pass 2
// copy failure (TestMigrateLegacySessionsWithholdsMarkerWhenACopyFails): the
// source file is still there and the next boot has to retry it.
func TestMigrateLegacySessionsWithholdsMarkerWhenASubdirCopyFails(t *testing.T) {
	root, src, global, workspace, blocked := markerSource(t)
	subDir := filepath.Join(src, "Users_x_project")
	writeSourceFile(t, filepath.Join(subDir, "chat-1.jsonl"), legacyMessageLog)
	writeLegacyMeta(t, subDir, "chat-1", workspace, "recovered chat")

	blockedRouter := func(string) string { return filepath.Join(blocked, "sessions") }
	n, err := MigrateLegacySessions(src, global, blockedRouter)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if n != 0 {
		t.Fatalf("imported %d sessions into a destination that could not be created", n)
	}
	if importMarkerExists(global, legacyRoutedHomeImportMarker) {
		t.Fatalf("import marker stamped after a failed project-subdir copy, so the session is never retried")
	}
	if importMarkerExists(global, legacyJsonlPassMarker) {
		t.Fatalf("jsonl pass marker stamped after a failed project-subdir copy")
	}

	// A repaired router must be given the chance to bring it over on the next boot.
	okDir := filepath.Join(root, "ok-sessions")
	n, err = MigrateLegacySessions(src, global, func(string) string { return okDir })
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if n != 1 {
		t.Fatalf("retry imported %d sessions, want 1", n)
	}
	got, err := LoadSession(filepath.Join(okDir, "chat-1.jsonl"))
	if err != nil {
		t.Fatalf("retry did not import the session: %v", err)
	}
	if len(got.Messages) != 4 || got.Messages[0].Content != "hello from v0.x" {
		t.Fatalf("retry imported %d messages, want the 4 of the source conversation: %+v", len(got.Messages), got.Messages)
	}
	if !importMarkerExists(global, legacyRoutedHomeImportMarker) {
		t.Fatalf("a complete retry left the marker unwritten, so every later boot would import again")
	}
}

// Same failure on the .jsonl.bak recovery path: the .jsonl was lost, so the
// .bak is the only copy of the conversation left in the source. A failed copy
// of it must not stamp the marker either.
func TestMigrateLegacySessionsWithholdsMarkerWhenABakCopyFails(t *testing.T) {
	root, src, global, workspace, blocked := markerSource(t)
	writeSourceFile(t, filepath.Join(src, "chat-1.jsonl.bak"), legacyMessageLog)
	writeLegacyMeta(t, src, "chat-1", workspace, "recovered from bak")

	blockedRouter := func(string) string { return filepath.Join(blocked, "sessions") }
	n, err := MigrateLegacySessions(src, global, blockedRouter)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if n != 0 {
		t.Fatalf("imported %d sessions into a destination that could not be created", n)
	}
	if importMarkerExists(global, legacyRoutedHomeImportMarker) {
		t.Fatalf("import marker stamped after a failed .jsonl.bak recovery copy, so the session is never retried")
	}

	okDir := filepath.Join(root, "ok-sessions")
	n, err = MigrateLegacySessions(src, global, func(string) string { return okDir })
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if n != 1 {
		t.Fatalf("retry imported %d sessions, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(okDir, "chat-1.jsonl")); err != nil {
		t.Fatalf("retry did not recover the .jsonl.bak session: %v", err)
	}
	if !importMarkerExists(global, legacyRoutedHomeImportMarker) {
		t.Fatalf("a complete retry left the marker unwritten, so every later boot would import again")
	}
}

// A hard destination failure for one session must not abort the run: the good
// session beside it still imports, and the marker stays unwritten because the
// failed one was left behind.
func TestMigrateLegacySessionsPassOneCopyFailureDoesNotStarveLaterPasses(t *testing.T) {
	root, src, global, workspace, blocked := markerSource(t)
	writeSourceFile(t, filepath.Join(src, "chat-1.events.jsonl"), legacyEventLog)
	writeLegacyMeta(t, src, "chat-1", workspace, "event session")
	// The unwritable Pass 1 destination belongs to a second, importable session.
	writeSourceFile(t, filepath.Join(src, "jsonl-1.jsonl"), legacyMessageLog)

	blockedRouter := func(string) string { return filepath.Join(blocked, "sessions") }
	n, err := MigrateLegacySessions(src, global, blockedRouter)
	if err != nil {
		t.Fatalf("a destination failure for one session aborted the whole run: %v", err)
	}
	if n != 1 {
		t.Fatalf("imported %d sessions, want 1 (the .jsonl-only session beside the failure)", n)
	}
	if _, err := os.Stat(filepath.Join(global, "jsonl-1.jsonl")); err != nil {
		t.Fatalf("Pass 2 was starved by a Pass 1 destination failure: %v", err)
	}
	if importMarkerExists(global, legacyRoutedHomeImportMarker) {
		t.Fatalf("import marker stamped although the Pass 1 session did not arrive")
	}

	// Next boot, with the destination repaired: the left-behind session imports.
	projRoot := filepath.Join(root, "projects")
	repaired := func(ws string) string { return filepath.Join(projRoot, filepath.Base(ws), "sessions") }
	n, err = MigrateLegacySessions(src, global, repaired)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if n != 1 {
		t.Fatalf("retry imported %d sessions, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(projRoot, filepath.Base(workspace), "sessions", "chat-1.jsonl")); err != nil {
		t.Fatalf("retry did not import the session that failed before: %v", err)
	}
	if !importMarkerExists(global, legacyRoutedHomeImportMarker) {
		t.Fatalf("a complete retry left the marker unwritten, so every later boot would import again")
	}
}

// Control: nothing fails, so every pass completes and the marker is stamped.
// The second run must still import nothing, even for an imported file the user
// deleted — that is what the marker is for.
func TestMigrateLegacySessionsStampsMarkerAfterACompleteImport(t *testing.T) {
	_, src, global, _, _ := markerSource(t)
	// One session per pass touched by the marker fix: Pass 1 events, Pass 2
	// .jsonl.bak recovery, Pass 3 project subdirectory.
	writeSourceFile(t, filepath.Join(src, "flat.events.jsonl"), legacyEventLog)
	writeSourceFile(t, filepath.Join(src, "bak-1.jsonl.bak"), legacyMessageLog)
	subDir := filepath.Join(src, "Users_x_project")
	writeSourceFile(t, filepath.Join(subDir, "proj-1.jsonl"), legacyMessageLog)
	writeLegacyMeta(t, subDir, "proj-1", "", "project session")

	n, err := MigrateLegacySessions(src, global, nil)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if n != 3 {
		t.Fatalf("complete import brought over %d sessions, want 3", n)
	}
	for _, name := range []string{"flat.jsonl", "bak-1.jsonl", "proj-1.jsonl"} {
		if _, err := os.Stat(filepath.Join(global, name)); err != nil {
			t.Fatalf("complete import did not bring over %s: %v", name, err)
		}
	}
	if !importMarkerExists(global, legacyRoutedHomeImportMarker) || !importMarkerExists(global, legacyJsonlPassMarker) {
		t.Fatalf("complete import did not stamp the one-time markers")
	}

	// The user deletes one import; the gate must still refuse to re-import.
	if err := os.Remove(filepath.Join(global, "flat.jsonl")); err != nil {
		t.Fatal(err)
	}
	n, err = MigrateLegacySessions(src, global, nil)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if n != 0 {
		t.Fatalf("second run imported %d sessions after the marker was stamped", n)
	}
	if _, err := os.Stat(filepath.Join(global, "flat.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("a deleted import reappeared on the second run")
	}
}
