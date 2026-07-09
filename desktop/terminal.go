package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"

	"reasonix/internal/config"
	"reasonix/internal/sandbox"
)

const (
	terminalOutputChannel = "terminal:output"
	terminalExitChannel   = "terminal:exit"
	maxTerminalsPerRoot   = 10
)

// TerminalSessionView is the frontend-facing snapshot of one interactive shell.
type TerminalSessionView struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Shell         string `json:"shell"`
	Cwd           string `json:"cwd"`
	WorkspaceRoot string `json:"workspaceRoot"`
	CreatedAt     int64  `json:"createdAt"`
	ExitCode      *int   `json:"exitCode,omitempty"`
	Running       bool   `json:"running"`
}

type terminalManager struct {
	app *App

	mu          sync.Mutex
	sessions    map[string]*terminalSession
	byWorkspace map[string][]string
}

type terminalSession struct {
	view   TerminalSessionView
	cmd    *exec.Cmd
	pty    *os.File
	closed chan struct{}
}

func newTerminalManager(app *App) *terminalManager {
	return &terminalManager{
		app:         app,
		sessions:    make(map[string]*terminalSession),
		byWorkspace: make(map[string][]string),
	}
}

func (m *terminalManager) closeAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		_ = m.closeTerminal(id)
	}
}

func (a *App) terminalReadOnly() bool {
	a.mu.RLock()
	tab := a.tabByIDLocked(a.activeTabID)
	readOnly := tab != nil && tab.ReadOnly
	a.mu.RUnlock()
	return readOnly
}

func (a *App) CreateTerminal(workspaceRoot, cwd, title, shellPrefer string) (string, error) {
	if a.terminalReadOnly() {
		return "", readOnlyChannelErr()
	}
	if a.terminals == nil {
		return "", errors.New("terminal manager not ready")
	}
	return a.terminals.create(workspaceRoot, cwd, title, shellPrefer)
}

func (a *App) WriteTerminal(id, data string) error {
	if a.terminals == nil {
		return errors.New("terminal manager not ready")
	}
	return a.terminals.write(id, data)
}

func (a *App) ResizeTerminal(id string, cols, rows int) error {
	if a.terminals == nil {
		return errors.New("terminal manager not ready")
	}
	return a.terminals.resize(id, cols, rows)
}

func (a *App) CloseTerminal(id string) error {
	if a.terminals == nil {
		return errors.New("terminal manager not ready")
	}
	return a.terminals.closeTerminal(id)
}

func (a *App) ListTerminals(workspaceRoot string) []TerminalSessionView {
	if a.terminals == nil {
		return nil
	}
	return a.terminals.list(workspaceRoot)
}

func (a *App) RenameTerminal(id, title string) error {
	if a.terminals == nil {
		return errors.New("terminal manager not ready")
	}
	return a.terminals.rename(id, strings.TrimSpace(title))
}

func (m *terminalManager) create(workspaceRoot, cwd, title, shellPrefer string) (string, error) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	cwd = strings.TrimSpace(cwd)
	if workspaceRoot == "" {
		return "", errors.New("workspace root is required")
	}
	if cwd == "" {
		cwd = workspaceRoot
	}
	cwd = filepathCleanOrFallback(cwd, workspaceRoot)

	m.mu.Lock()
	if len(m.byWorkspace[workspaceRoot]) >= maxTerminalsPerRoot {
		m.mu.Unlock()
		return "", fmt.Errorf("terminal session limit reached (%d)", maxTerminalsPerRoot)
	}
	m.mu.Unlock()

	sh := m.resolveInteractiveShell(workspaceRoot, shellPrefer)
	argv := interactiveShellArgv(sh)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	)

	ptyFile, err := pty.Start(cmd)
	if err != nil {
		return "", fmt.Errorf("start terminal: %w", err)
	}

	id, err := newTerminalID()
	if err != nil {
		_ = ptyFile.Close()
		_ = cmd.Process.Kill()
		return "", err
	}

	displayTitle := strings.TrimSpace(title)
	if displayTitle == "" {
		displayTitle = shellDisplayName(sh)
	}

	s := &terminalSession{
		view: TerminalSessionView{
			ID:            id,
			Title:         displayTitle,
			Shell:         sh.Path,
			Cwd:           cwd,
			WorkspaceRoot: workspaceRoot,
			CreatedAt:     time.Now().UnixMilli(),
			Running:       true,
		},
		cmd:    cmd,
		pty:    ptyFile,
		closed: make(chan struct{}),
	}

	m.mu.Lock()
	m.sessions[id] = s
	m.byWorkspace[workspaceRoot] = append(m.byWorkspace[workspaceRoot], id)
	m.mu.Unlock()

	m.startIOLoops(id, s)
	return id, nil
}

func (m *terminalManager) write(id, data string) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("terminal session not found: %s", id)
	}
	if !s.view.Running {
		return fmt.Errorf("terminal session exited: %s", id)
	}
	_, err := s.pty.Write([]byte(data))
	return err
}

func (m *terminalManager) resize(id string, cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	m.mu.Lock()
	s, ok := m.sessions[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("terminal session not found: %s", id)
	}
	return pty.Setsize(s.pty, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

func (m *terminalManager) closeTerminal(id string) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	delete(m.sessions, id)
	root := s.view.WorkspaceRoot
	ids := m.byWorkspace[root]
	filtered := ids[:0]
	for _, existing := range ids {
		if existing != id {
			filtered = append(filtered, existing)
		}
	}
	if len(filtered) == 0 {
		delete(m.byWorkspace, root)
	} else {
		m.byWorkspace[root] = filtered
	}
	m.mu.Unlock()

	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	_ = s.pty.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return nil
}

func (m *terminalManager) list(workspaceRoot string) []TerminalSessionView {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := m.byWorkspace[workspaceRoot]
	out := make([]TerminalSessionView, 0, len(ids))
	for _, id := range ids {
		if s, ok := m.sessions[id]; ok {
			out = append(out, s.view)
		}
	}
	return out
}

func (m *terminalManager) rename(id, title string) error {
	if title == "" {
		return errors.New("title is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return fmt.Errorf("terminal session not found: %s", id)
	}
	s.view.Title = title
	return nil
}

func (m *terminalManager) startIOLoops(id string, s *terminalSession) {
	go m.readLoop(id, s)
	go m.waitLoop(id, s)
}

func (m *terminalManager) readLoop(id string, s *terminalSession) {
	buf := make([]byte, 4096)
	for {
		select {
		case <-s.closed:
			return
		default:
		}
		n, err := s.pty.Read(buf)
		if n > 0 {
			m.emitOutput(id, buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (m *terminalManager) waitLoop(id string, s *terminalSession) {
	err := s.cmd.Wait()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	m.mu.Lock()
	if cur, ok := m.sessions[id]; ok {
		cur.view.Running = false
		cur.view.ExitCode = &exitCode
	}
	m.mu.Unlock()

	m.emitExit(id, exitCode)
}

func (m *terminalManager) emitOutput(id string, data []byte) {
	if m.app == nil || len(data) == 0 {
		return
	}
	m.app.emitRuntimeEvent(terminalOutputChannel, map[string]any{
		"id":   id,
		"data": base64.StdEncoding.EncodeToString(data),
	})
}

func (m *terminalManager) emitExit(id string, exitCode int) {
	if m.app == nil {
		return
	}
	m.app.emitRuntimeEvent(terminalExitChannel, map[string]any{
		"id":       id,
		"exitCode": exitCode,
	})
}

func (m *terminalManager) resolveInteractiveShell(workspaceRoot, sessionPrefer string) sandbox.Shell {
	prefer, path := "", ""
	if cfg, err := config.LoadForRoot(workspaceRoot); err == nil {
		prefer = cfg.Tools.Shell.Prefer
		path = cfg.Tools.Shell.Path
	} else if cfg, err := config.Load(); err == nil {
		prefer = cfg.Tools.Shell.Prefer
		path = cfg.Tools.Shell.Path
	}
	return resolveInteractiveShell(prefer, path, sessionPrefer)
}

// resolveInteractiveShell picks the user's interactive login shell by default.
// Agent bash tooling still uses sandbox.ResolveShell (bash-first); the integrated
// terminal follows $SHELL on auto, matching Cursor/VS Code behaviour.
func resolveInteractiveShell(settingsPrefer, settingsPath, sessionPrefer string) sandbox.Shell {
	sessionPrefer = strings.TrimSpace(strings.ToLower(sessionPrefer))
	if sessionPrefer != "" {
		if sh, ok := shellForPrefer(sessionPrefer); ok {
			return sh
		}
	}
	if p := strings.TrimSpace(settingsPath); p != "" {
		return shellFromPath(p)
	}
	prefer := strings.TrimSpace(strings.ToLower(settingsPrefer))
	switch prefer {
	case "bash":
		if sh, ok := lookupShell("bash"); ok {
			return sh
		}
	case "zsh":
		if sh, ok := lookupShell("zsh"); ok {
			return sh
		}
	case "powershell", "pwsh":
		return sandbox.ResolveShell(prefer, "", nil)
	}
	return loginShell()
}

func shellForPrefer(prefer string) (sandbox.Shell, bool) {
	switch prefer {
	case "login", "default", "auto":
		return loginShell(), true
	case "bash", "zsh", "sh", "fish":
		return lookupShell(prefer)
	case "powershell", "pwsh":
		return sandbox.ResolveShell(prefer, "", nil), true
	default:
		if strings.Contains(prefer, "/") || strings.Contains(prefer, `\`) {
			if _, err := os.Stat(prefer); err == nil {
				return shellFromPath(prefer), true
			}
		}
		return lookupShell(prefer)
	}
}

func loginShell() sandbox.Shell {
	if p := strings.TrimSpace(os.Getenv("SHELL")); p != "" {
		if _, err := os.Stat(p); err == nil {
			return shellFromPath(p)
		}
	}
	if goruntime.GOOS == "darwin" {
		if sh, ok := lookupShell("zsh"); ok {
			return sh
		}
	}
	if sh, ok := lookupShell("bash"); ok {
		return sh
	}
	return sandbox.ResolveShell("auto", "", nil)
}

func lookupShell(name string) (sandbox.Shell, bool) {
	p, err := exec.LookPath(name)
	if err != nil {
		return sandbox.Shell{}, false
	}
	return shellFromPath(p), true
}

func shellFromPath(path string) sandbox.Shell {
	base := strings.ToLower(strings.TrimSuffix(filepathBase(path), ".exe"))
	kind := sandbox.ShellBash
	if strings.Contains(base, "powershell") || base == "pwsh" {
		kind = sandbox.ShellPowerShell
	}
	return sandbox.Shell{Kind: kind, Path: path}
}

func interactiveShellArgv(sh sandbox.Shell) []string {
	if sh.Kind == sandbox.ShellPowerShell {
		return []string{sh.Path, "-NoLogo"}
	}
	base := strings.ToLower(strings.TrimSuffix(filepathBase(sh.Path), ".exe"))
	switch base {
	case "bash", "zsh", "sh", "ksh":
		return []string{sh.Path, "-l"}
	default:
		return []string{sh.Path}
	}
}

func shellDisplayName(sh sandbox.Shell) string {
	base := filepathBase(sh.Path)
	if base == "" {
		if sh.Kind == sandbox.ShellPowerShell {
			return "powershell"
		}
		return "bash"
	}
	return strings.TrimSuffix(base, ".exe")
}

func newTerminalID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "term-" + hex.EncodeToString(b[:]), nil
}

func filepathCleanOrFallback(path, fallback string) string {
	clean := strings.TrimSpace(path)
	if clean == "" {
		return fallback
	}
	return filepath.Clean(clean)
}

func filepathBase(path string) string {
	return filepath.Base(path)
}
