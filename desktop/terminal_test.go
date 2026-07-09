package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/sandbox"
)

func TestInteractiveShellArgv(t *testing.T) {
	t.Parallel()
	bash := interactiveShellArgv(sandbox.Shell{Kind: sandbox.ShellBash, Path: "/bin/bash"})
	if len(bash) != 2 || bash[0] != "/bin/bash" || bash[1] != "-l" {
		t.Fatalf("bash argv = %#v", bash)
	}
	zsh := interactiveShellArgv(sandbox.Shell{Kind: sandbox.ShellBash, Path: "/bin/zsh"})
	if len(zsh) != 2 || zsh[0] != "/bin/zsh" || zsh[1] != "-l" {
		t.Fatalf("zsh argv = %#v", zsh)
	}
	ps := interactiveShellArgv(sandbox.Shell{Kind: sandbox.ShellPowerShell, Path: "C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe"})
	if len(ps) < 2 || ps[1] != "-NoLogo" {
		t.Fatalf("powershell argv = %#v", ps)
	}
}

func TestResolveInteractiveShellPrefersLoginShell(t *testing.T) {
	shellPath := os.Getenv("SHELL")
	if shellPath == "" {
		shellPath = "/bin/bash"
	}
	if _, err := os.Stat(shellPath); err != nil {
		shellPath = "/usr/bin/bash"
	}
	t.Setenv("SHELL", shellPath)
	sh := resolveInteractiveShell("auto", "", "")
	if sh.Path != shellPath {
		t.Fatalf("login shell = %#v, want %s", sh, shellPath)
	}
}

func TestResolveInteractiveShellSessionOverride(t *testing.T) {
	shellPath := os.Getenv("SHELL")
	if shellPath == "" {
		shellPath = "/bin/bash"
	}
	t.Setenv("SHELL", shellPath)
	sh := resolveInteractiveShell("auto", "", "bash")
	if !strings.Contains(sh.Path, "bash") {
		t.Fatalf("session override = %#v, want bash", sh)
	}
}

func TestTerminalManagerListRenameClose(t *testing.T) {
	t.Parallel()
	app := &App{terminals: newTerminalManager(&App{})}
	t.Cleanup(app.terminals.closeAll)
	root := t.TempDir()

	id, err := app.terminals.create(root, root, "test shell", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	list := app.terminals.list(root)
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("list = %#v, want one session %q", list, id)
	}
	if err := app.terminals.rename(id, "renamed"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	list = app.terminals.list(root)
	if list[0].Title != "renamed" {
		t.Fatalf("title = %q", list[0].Title)
	}
	if err := app.terminals.closeTerminal(id); err != nil {
		t.Fatalf("close: %v", err)
	}
	if len(app.terminals.list(root)) != 0 {
		t.Fatal("expected empty list after close")
	}
}

func TestTerminalManagerSessionLimit(t *testing.T) {
	t.Parallel()
	mgr := newTerminalManager(&App{})
	t.Cleanup(mgr.closeAll)
	root := t.TempDir()
	for i := 0; i < maxTerminalsPerRoot; i++ {
		if _, err := mgr.create(root, root, "", ""); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	if _, err := mgr.create(root, root, "", ""); err == nil {
		t.Fatal("expected session limit error")
	} else if !strings.Contains(err.Error(), "limit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTerminalSessionExits(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns shell process")
	}
	app := &App{terminals: newTerminalManager(&App{})}
	t.Cleanup(app.terminals.closeAll)
	root := t.TempDir()
	id, err := app.terminals.create(root, root, "exit-test", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := app.terminals.write(id, "exit 0\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		list := app.terminals.list(root)
		if len(list) == 1 && !list[0].Running && list[0].ExitCode != nil && *list[0].ExitCode == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	list := app.terminals.list(root)
	t.Fatalf("session did not exit cleanly: %#v", list)
}

// TestTerminalManagerWriteDuringExitIsRaceFree drives concurrent write() calls
// against a session that is exiting at the same time, so waitLoop's locked
// write to view.Running races with any unlocked read of it in write(). Run
// with -race: this only fails by detector, not by assertion.
func TestTerminalManagerWriteDuringExitIsRaceFree(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns shell process")
	}
	app := &App{terminals: newTerminalManager(&App{})}
	t.Cleanup(app.terminals.closeAll)
	root := t.TempDir()
	id, err := app.terminals.create(root, root, "race-test", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	stop := time.Now().Add(500 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for time.Now().Before(stop) {
			_ = app.terminals.write(id, "echo hi\n")
		}
	}()

	// Give the writer goroutine a head start, then exit mid-flight so its
	// writes straddle the moment waitLoop flips Running under lock.
	time.Sleep(20 * time.Millisecond)
	if err := app.terminals.write(id, "exit 0\n"); err != nil {
		t.Fatalf("write exit: %v", err)
	}
	<-done

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		list := app.terminals.list(root)
		if len(list) == 1 && !list[0].Running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("session did not exit within deadline")
}
