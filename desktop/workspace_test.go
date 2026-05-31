package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
)

// --- workspaceStatePath ---

func TestWorkspaceStatePath(t *testing.T) {
	// workspaceStatePath depends on config.MemoryUserDir() which needs a
	// config dir. We just verify it returns a consistent path.
	p1 := workspaceStatePath()
	p2 := workspaceStatePath()
	if p1 != p2 {
		t.Errorf("workspaceStatePath not stable: %q vs %q", p1, p2)
	}
	if p1 != "" && filepath.Base(p1) != "desktop-workspace" {
		t.Errorf("workspaceStatePath should end with desktop-workspace, got %q", p1)
	}
}

// --- saveWorkspace / loadWorkspace round-trip ---

func TestSaveLoadWorkspaceRoundTrip(t *testing.T) {
	// workspaceStatePath() lives under config.MemoryUserDir(), which resolves via
	// os.UserConfigDir() — rooted at HOME. Point HOME at a temp dir so the path
	// resolves to a real, writable location and the save→load round-trip actually
	// exercises persistence instead of silently no-opping when no config dir
	// happens to exist in the environment.
	t.Setenv("HOME", t.TempDir())
	if workspaceStatePath() == "" {
		t.Fatal("workspaceStatePath() is empty after pointing HOME at a temp dir")
	}

	dir := t.TempDir()
	saveWorkspace(dir)
	if got := loadWorkspace(); got != dir {
		t.Errorf("loadWorkspace = %q, want %q", got, dir)
	}
}

// --- cwdWritable ---

func TestCwdWritable(t *testing.T) {
	// In a normal test environment, cwd should be writable.
	if !cwdWritable() {
		t.Error("cwd should be writable in test environment")
	}
}

func TestCwdWritableInTempDir(t *testing.T) {
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	dir := t.TempDir()
	os.Chdir(dir)
	if !cwdWritable() {
		t.Error("temp dir should be writable")
	}
}

func TestAllowedOpenPathRestrictsToSafeRoots(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	t.Chdir(workspace)

	insideDir := filepath.Join(workspace, "inside")
	if err := os.Mkdir(insideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := allowedOpenPath(insideDir); err != nil || got == "" {
		t.Fatalf("workspace directory should be allowed, got %q err=%v", got, err)
	}
	appBundle := filepath.Join(workspace, "Tool.app")
	if err := os.Mkdir(appBundle, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := allowedOpenPath(appBundle); err == nil {
		t.Fatal("application bundles should not be opened")
	}

	insideFile := filepath.Join(workspace, "file.txt")
	if err := os.WriteFile(insideFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := allowedOpenPath(insideFile); err == nil {
		t.Fatal("workspace file should not be opened directly")
	}

	outsideDir := t.TempDir()
	if _, err := allowedOpenPath(outsideDir); err == nil {
		t.Fatal("directory outside safe roots should be rejected")
	}

	logDir := config.ManagerRunDir()
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(logDir, "run.jsonl")
	if err := os.WriteFile(logPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := allowedOpenPath(logPath); err != nil || got == "" {
		t.Fatalf("manager run log should be allowed, got %q err=%v", got, err)
	}
}

func TestAllowedOpenPathAllowsRegisteredGitWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDesktop(t, repo, "init")
	readme := filepath.Join(repo, "README.md")
	if err := os.WriteFile(readme, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitDesktop(t, repo, "add", "README.md")
	runGitDesktop(t, repo, "-c", "user.name=Reasonix Test", "-c", "user.email=test@example.com", "commit", "-m", "init")
	wt := filepath.Join(parent, "wt-feature")
	runGitDesktop(t, repo, "worktree", "add", "-b", "feature/open-path", wt)
	t.Chdir(repo)

	if got, err := allowedOpenPath(wt); err != nil || got == "" {
		t.Fatalf("registered git worktree should be allowed, got %q err=%v", got, err)
	}
	if _, err := allowedOpenPath(filepath.Join(wt, "README.md")); err == nil {
		t.Fatal("worktree files should not be opened directly")
	}
}

func runGitDesktop(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// --- settings_app.go helpers ---
// These are unexported but in the same package, so we can test them.

func TestOrDefault(t *testing.T) {
	if orDefault("", "fallback") != "fallback" {
		t.Error("empty should return default")
	}
	if orDefault("value", "fallback") != "value" {
		t.Error("non-empty should return value")
	}
}

func TestTrimList(t *testing.T) {
	got := trimList([]string{"  a  ", "", " b ", "  "})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("trimList = %v", got)
	}
}

func TestTrimListEmpty(t *testing.T) {
	got := trimList(nil)
	if len(got) != 0 {
		t.Errorf("nil = %v, want empty", got)
	}
}

func TestNonNil(t *testing.T) {
	if got := nonNil(nil); got == nil || len(got) != 0 {
		t.Errorf("nonNil(nil) = %v, want empty non-nil", got)
	}
	s := []string{"a"}
	if got := nonNil(s); got[0] != "a" {
		t.Errorf("nonNil should pass through")
	}
}
