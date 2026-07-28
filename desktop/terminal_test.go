package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/config"
)

func TestResolveTerminalStartDirUsesFilesystemTypeAndContainsSymlinks(t *testing.T) {
	root := t.TempDir()
	dottedDir := filepath.Join(root, "config.d")
	if err := os.Mkdir(dottedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	license := filepath.Join(root, "LICENSE")
	if err := os.WriteFile(license, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := canonicalDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	canonicalDottedDir, err := canonicalDirectory(dottedDir)
	if err != nil {
		t.Fatal(err)
	}

	if got, err := resolveTerminalStartDir(root, "config.d"); err != nil || got != canonicalDottedDir {
		t.Fatalf("dotted directory = %q, %v; want %q", got, err, canonicalDottedDir)
	}
	if got, err := resolveTerminalStartDir(root, "LICENSE"); err != nil || got != canonicalRoot {
		t.Fatalf("extensionless file = %q, %v; want %q", got, err, canonicalRoot)
	}
	if _, err := resolveTerminalStartDir(root, "../outside"); !errors.Is(err, errTerminalOutside) {
		t.Fatalf("parent traversal error = %v, want errTerminalOutside", err)
	}
	if _, err := resolveTerminalStartDir(root, root); !errors.Is(err, errTerminalOutside) {
		t.Fatalf("absolute path error = %v, want errTerminalOutside", err)
	}

	outside := t.TempDir()
	link := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := resolveTerminalStartDir(root, "outside-link"); !errors.Is(err, errTerminalOutside) {
		t.Fatalf("escaping directory symlink error = %v, want errTerminalOutside", err)
	}
}

func TestResolveTerminalCommandTrustsOnlyUserConfigPath(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	t.Setenv("REASONIX_SAFE_MODE", "")
	root := t.TempDir()
	projectShell := testExecutable(t, root, "project-shell")
	projectConfig := "[tools.shell]\nprefer = \"bash\"\npath = " + strconv.Quote(projectShell) + "\n"
	if err := os.WriteFile(filepath.Join(root, "reasonix.toml"), []byte(projectConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	command, err := resolveTerminalCommand(root, "default")
	if err != nil {
		t.Fatal(err)
	}
	if command.path == projectShell {
		t.Fatal("project reasonix.toml selected the integrated terminal executable")
	}

	userShell := testExecutable(t, t.TempDir(), "user-shell")
	userConfig := "[tools.shell]\nprefer = \"bash\"\npath = " + strconv.Quote(userShell) + "\n"
	userConfigPath := config.UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(userConfigPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userConfigPath, []byte(userConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	command, err = resolveTerminalCommand(root, "default")
	if err != nil {
		t.Fatal(err)
	}
	if command.path != userShell {
		t.Fatalf("user-configured shell = %q, want %q", command.path, userShell)
	}
	if _, err := resolveTerminalCommand(root, userShell); err == nil || !strings.Contains(err.Error(), "unsupported terminal shell") {
		t.Fatalf("renderer path override error = %v, want unsupported shell", err)
	}
}

func testExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTerminalTargetRejectsStaleAndReadOnlyTabs(t *testing.T) {
	app := NewApp()
	root := t.TempDir()
	tab := &WorkspaceTab{ID: "active", Scope: "project", WorkspaceRoot: root, ReadOnly: true}
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	view, err := app.TerminalWorkspaceForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !view.ReadOnly || view.Sessions == nil || view.Shells == nil {
		t.Fatalf("read-only workspace view = %+v, want non-nil arrays", view)
	}
	if _, err := app.CreateTerminalForTab(tab.ID, ".", "default"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only create error = %v", err)
	}
	if _, err := app.TerminalWorkspaceForTab("stale"); !errors.Is(err, errTerminalStaleTab) {
		t.Fatalf("stale tab error = %v, want errTerminalStaleTab", err)
	}
}

func TestTerminalTargetScopesSessionsToTheChatTab(t *testing.T) {
	app := NewApp()
	root := t.TempDir()
	app.tabs["one"] = &WorkspaceTab{ID: "one", Scope: "project", WorkspaceRoot: root}
	app.tabs["two"] = &WorkspaceTab{ID: "two", Scope: "project", WorkspaceRoot: root}
	app.tabOrder = []string{"one", "two"}
	app.activeTabID = "one"

	first, err := app.terminalTargetForTab("one", false)
	if err != nil {
		t.Fatal(err)
	}
	app.activeTabID = "two"
	second, err := app.terminalTargetForTab("two", false)
	if err != nil {
		t.Fatal(err)
	}
	if first.workspaceRoot != second.workspaceRoot {
		t.Fatalf("same project root changed: %q != %q", first.workspaceRoot, second.workspaceRoot)
	}
	if first.workspaceKey == second.workspaceKey {
		t.Fatalf("terminal scope key shared across chat tabs: %q", first.workspaceKey)
	}
}

func TestEmptyTerminalWorkspaceViewSerializesArrays(t *testing.T) {
	view := emptyTerminalWorkspaceView()
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, `"sessions":[]`) || !strings.Contains(text, `"shells":[]`) {
		t.Fatalf("terminal workspace JSON = %s, want [] arrays", text)
	}
}

type fakeTerminalWait struct {
	code int
	err  error
}

type fakeTerminalProcess struct {
	waitResult chan fakeTerminalWait
	closed     chan struct{}
	closeOnce  sync.Once

	mu      sync.Mutex
	writes  []byte
	resizes [][2]int
}

func newFakeTerminalProcess() *fakeTerminalProcess {
	return &fakeTerminalProcess{
		waitResult: make(chan fakeTerminalWait, 1),
		closed:     make(chan struct{}),
	}
}

func (p *fakeTerminalProcess) Read([]byte) (int, error) {
	<-p.closed
	return 0, io.EOF
}

func (p *fakeTerminalProcess) Write(data []byte) (int, error) {
	p.mu.Lock()
	p.writes = append(p.writes, data...)
	p.mu.Unlock()
	return len(data), nil
}

func (p *fakeTerminalProcess) Resize(cols, rows int) error {
	p.mu.Lock()
	p.resizes = append(p.resizes, [2]int{cols, rows})
	p.mu.Unlock()
	return nil
}

func (p *fakeTerminalProcess) Wait() (int, error) {
	select {
	case result := <-p.waitResult:
		return result.code, result.err
	case <-p.closed:
		return -1, errors.New("closed")
	}
}

func (p *fakeTerminalProcess) Close() error {
	p.closeOnce.Do(func() { close(p.closed) })
	return nil
}

func TestTerminalManagerCountsConcurrentStartsTowardLimit(t *testing.T) {
	manager := newTerminalManager(nil)
	entered := make(chan struct{}, maxTerminalsPerWorkspace+1)
	release := make(chan struct{})
	manager.start = func(terminalStartSpec) (terminalProcess, error) {
		entered <- struct{}{}
		<-release
		return newFakeTerminalProcess(), nil
	}

	type result struct{ err error }
	results := make(chan result, maxTerminalsPerWorkspace+1)
	for i := 0; i < maxTerminalsPerWorkspace+1; i++ {
		go func() {
			_, err := manager.create("tab", "workspace", ".", terminalCommand{path: "shell", label: "shell"})
			results <- result{err: err}
		}()
	}
	for i := 0; i < maxTerminalsPerWorkspace; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("terminal starts did not reach the concurrency barrier")
		}
	}
	close(release)

	succeeded, limited := 0, 0
	for i := 0; i < maxTerminalsPerWorkspace+1; i++ {
		result := <-results
		switch {
		case result.err == nil:
			succeeded++
		case strings.Contains(result.err.Error(), "session limit"):
			limited++
		default:
			t.Fatalf("unexpected create error: %v", result.err)
		}
	}
	if succeeded != maxTerminalsPerWorkspace || limited != 1 {
		t.Fatalf("create results: succeeded=%d limited=%d", succeeded, limited)
	}
	manager.closeAll()
}

func TestTerminalManagerCloseAndNaturalExit(t *testing.T) {
	t.Run("close cleans up process", func(t *testing.T) {
		manager := newTerminalManager(nil)
		proc := newFakeTerminalProcess()
		manager.start = func(terminalStartSpec) (terminalProcess, error) { return proc, nil }
		view, err := manager.create("tab", "workspace", ".", terminalCommand{path: "shell", label: "shell"})
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.closeTerminal("workspace", view.ID); err != nil {
			t.Fatal(err)
		}
		select {
		case <-proc.closed:
		default:
			t.Fatal("terminal process was not closed")
		}
		if got := manager.list("workspace"); len(got) != 0 {
			t.Fatalf("sessions after close = %+v", got)
		}
	})

	t.Run("natural exit updates view", func(t *testing.T) {
		manager := newTerminalManager(nil)
		proc := newFakeTerminalProcess()
		manager.start = func(terminalStartSpec) (terminalProcess, error) { return proc, nil }
		view, err := manager.create("tab", "workspace", ".", terminalCommand{path: "shell", label: "shell"})
		if err != nil {
			t.Fatal(err)
		}
		manager.mu.Lock()
		done := manager.sessions[view.ID].done
		manager.mu.Unlock()
		proc.waitResult <- fakeTerminalWait{code: 7, err: errors.New("exit status 7")}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("terminal wait loop did not finish")
		}
		sessions := manager.list("workspace")
		if len(sessions) != 1 || sessions[0].Running || sessions[0].ExitCode == nil || *sessions[0].ExitCode != 7 {
			t.Fatalf("session after exit = %+v", sessions)
		}
		manager.closeAll()
	})
}

func TestTerminalManagerClosesOnlyTheClosingTabAndBoundsOutput(t *testing.T) {
	manager := newTerminalManager(nil)
	procs := make([]*fakeTerminalProcess, 0, 2)
	manager.start = func(terminalStartSpec) (terminalProcess, error) {
		proc := newFakeTerminalProcess()
		procs = append(procs, proc)
		return proc, nil
	}
	first, err := manager.create("tab-one", "tab-one\x00workspace", ".", terminalCommand{path: "shell", label: "shell"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.create("tab-two", "tab-two\x00workspace", ".", terminalCommand{path: "shell", label: "shell"})
	if err != nil {
		t.Fatal(err)
	}

	manager.mu.Lock()
	manager.sessions[first.ID].output = appendTerminalSnapshot(
		[]byte(strings.Repeat("x", maxTerminalSnapshotBytes)),
		[]byte("y"),
	)
	manager.mu.Unlock()
	if got := manager.snapshot("tab-one\x00workspace", first.ID); len(got) != maxTerminalSnapshotBytes || !strings.HasSuffix(got, "y") {
		t.Fatalf("bounded snapshot = len %d suffix %q", len(got), got[len(got)-1:])
	}
	manager.mu.Lock()
	manager.sessions[first.ID].output = appendTerminalSnapshot(nil, []byte(strings.Repeat("z", maxTerminalSnapshotBytes+1)))
	manager.mu.Unlock()
	if got := manager.snapshot("tab-one\x00workspace", first.ID); len(got) != maxTerminalSnapshotBytes || !strings.HasPrefix(got, "z") {
		t.Fatalf("large bounded snapshot = len %d prefix %q", len(got), got[:1])
	}

	manager.closeForTab("tab-one")
	select {
	case <-procs[0].closed:
	case <-time.After(time.Second):
		t.Fatal("closing tab did not close its terminal")
	}
	select {
	case <-procs[1].closed:
		t.Fatal("closing tab closed another tab's terminal")
	default:
	}
	if got := manager.list("tab-one\x00workspace"); len(got) != 0 {
		t.Fatalf("closed tab sessions = %+v", got)
	}
	if got := manager.list("tab-two\x00workspace"); len(got) != 1 || got[0].ID != second.ID {
		t.Fatalf("surviving tab sessions = %+v", got)
	}
	manager.closeAll()
}
