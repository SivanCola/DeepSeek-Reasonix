package builtin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"reasonix/internal/persistentshell"
	"reasonix/internal/sandbox"
)

func persistentBash(t *testing.T, workDir string) bash {
	t.Helper()
	m := persistentshell.New()
	m.Retain()
	t.Cleanup(m.Release)
	sh := sandbox.ResolveShell("", "", nil)
	return bash{
		sb:         sandbox.Spec{Mode: "off"},
		shell:      sh,
		workDir:    workDir,
		timeout:    8 * time.Second,
		persistent: m,
	}
}

func TestBashPersistentKeepsWorkingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX persistent bash")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	b := persistentBash(t, dir)
	ctx := fullAccessBashTestContext(t.Context())
	if _, err := b.Execute(ctx, argsJSON(t, map[string]any{"command": "cd sub"})); err != nil {
		t.Fatalf("cd: %v", err)
	}
	out, err := b.Execute(ctx, argsJSON(t, map[string]any{"command": "pwd"}))
	if err != nil {
		t.Fatalf("pwd: %v (%q)", err, out)
	}
	want, _ := filepath.EvalSymlinks(sub)
	got := strings.TrimSpace(out)
	if resolved, rerr := filepath.EvalSymlinks(got); rerr == nil {
		got = resolved
	}
	if got != want && !strings.Contains(out, "sub") {
		t.Fatalf("pwd=%q want %q", out, want)
	}
}

func TestBashWithoutPersistentDoesNotKeepCwd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX one-shot bash")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	b := bash{
		sb:      sandbox.Spec{Mode: "off"},
		shell:   sandbox.ResolveShell("", "", nil),
		workDir: dir,
		timeout: 8 * time.Second,
	}
	ctx := fullAccessBashTestContext(t.Context())
	if _, err := b.Execute(ctx, argsJSON(t, map[string]any{"command": "cd sub"})); err != nil {
		t.Fatalf("cd: %v", err)
	}
	out, err := b.Execute(ctx, argsJSON(t, map[string]any{"command": "pwd"}))
	if err != nil {
		t.Fatalf("pwd: %v (%q)", err, out)
	}
	got := strings.TrimSpace(out)
	if resolved, rerr := filepath.EvalSymlinks(got); rerr == nil {
		got = resolved
	}
	want, _ := filepath.EvalSymlinks(dir)
	if got != want {
		t.Fatalf("one-shot pwd=%q want workspace %q", out, want)
	}
}

func TestBashPersistentSchemaUnchanged(t *testing.T) {
	plain := bash{}.Schema()
	withPTY := bash{persistent: persistentshell.New()}.Schema()
	if string(plain) != string(withPTY) {
		t.Fatalf("persistent PTY must not change bash schema\nplain=%s\nwith=%s", plain, withPTY)
	}
	if (bash{}).Description() != (bash{persistent: persistentshell.New()}).Description() {
		t.Fatal("persistent PTY must not change bash description")
	}
	var schema map[string]any
	if err := json.Unmarshal(plain, &schema); err != nil {
		t.Fatal(err)
	}
}

func TestBashPersistentSkipsBackgroundAndWriteEscalation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX persistent bash")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	extra := t.TempDir()
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	b := persistentBash(t, dir)
	ctx := fullAccessBashTestContext(t.Context())
	if _, err := b.Execute(ctx, argsJSON(t, map[string]any{"command": "cd sub"})); err != nil {
		t.Fatalf("cd: %v", err)
	}
	out, err := b.Execute(ctx, argsJSON(t, map[string]any{
		"command":               "pwd",
		"additional_write_dirs": []string{extra},
		"justification":         "test one-shot fallback",
	}))
	if err != nil {
		t.Fatalf("escalated pwd: %v (%q)", err, out)
	}
	got := strings.TrimSpace(out)
	if resolved, rerr := filepath.EvalSymlinks(got); rerr == nil {
		got = resolved
	}
	wantSub, _ := filepath.EvalSymlinks(sub)
	if got == wantSub {
		t.Fatalf("additional_write_dirs must not reuse persistent cwd, got %q", out)
	}
}
