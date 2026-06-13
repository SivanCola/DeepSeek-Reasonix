// Package daemon implements the Reasonix background agent service. It holds a
// session registry, exposes a localhost-only HTTP control API, and manages
// lifecycle transitions (interrupted recovery, goal continuation). A single
// daemon instance per user is enforced via a lockfile in the sessions directory.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// DefaultAddr is the localhost-only address the daemon listens on.
const DefaultAddr = "127.0.0.1:19840"

// Daemon is the long-running background service that tracks sessions and
// provides the control API.
type Daemon struct {
	addr       string
	sessionDir string
	logger     *slog.Logger

	mu          sync.RWMutex
	registry    map[string]*SessionEntry // session ID → entry
	server      *http.Server
	scheduler   *Scheduler
	fileWatcher *FileWatcher
	webhookCfg  *WebhookConfig
	lockPath    string
}

// SessionEntry is one tracked session in the registry.
type SessionEntry struct {
	ID          string              `json:"id"`
	Path        string              `json:"path"`
	Runtime     agent.RuntimeMeta   `json:"runtime"`
	DiscoveredAt time.Time          `json:"discovered_at"`
}

// StatusResponse is the JSON body of GET /status.
type StatusResponse struct {
	Status   string `json:"status"`
	Addr     string `json:"addr"`
	Sessions int    `json:"sessions"`
	Uptime   string `json:"uptime"`
	PID      int    `json:"pid"`
}

// SessionsResponse is the JSON body of GET /sessions.
type SessionsResponse struct {
	Sessions []SessionView `json:"sessions"`
}

// SessionView is the public representation of a session in the API.
type SessionView struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	GoalText   string `json:"goal_text,omitempty"`
	GoalStatus string `json:"goal_status,omitempty"`
	RunStatus  string `json:"run_status,omitempty"`
}

// Options configures daemon creation.
type Options struct {
	Addr       string
	SessionDir string
	Logger     *slog.Logger
	Webhook    *WebhookConfig
}

// New creates a Daemon but does not start it.
func New(opts Options) *Daemon {
	addr := opts.Addr
	if addr == "" {
		addr = DefaultAddr
	}
	sessionDir := opts.SessionDir
	if sessionDir == "" {
		sessionDir = config.SessionDir()
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Daemon{
		addr:       addr,
		sessionDir: sessionDir,
		logger:     logger,
		webhookCfg: opts.Webhook,
		registry:   make(map[string]*SessionEntry),
	}
}

// Start acquires the lockfile, scans existing sessions, recovers interrupted
// state, starts the scheduler, and starts the HTTP server. It blocks until ctx
// is cancelled or the server fails.
func (d *Daemon) Start(ctx context.Context) error {
	if err := d.acquireLock(); err != nil {
		return fmt.Errorf("daemon already running or lock error: %w", err)
	}
	defer d.releaseLock()

	d.scanSessions()
	d.recoverInterrupted()

	// Start the scheduler.
	d.scheduler = NewScheduler(d, d.logger)
	go d.scheduler.Start(ctx)

	// Start the file watcher.
	d.fileWatcher = NewFileWatcher(d, d.logger)
	go d.fileWatcher.Start(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", d.handleStatus)
	mux.HandleFunc("GET /sessions", d.handleSessions)
	mux.HandleFunc("POST /continue-goal", d.handleContinueGoal)
	mux.HandleFunc("POST /stop", d.handleStop)
	mux.HandleFunc("POST /schedule", d.handleSchedule)
	mux.HandleFunc("POST /webhook", d.handleWebhook)
	mux.HandleFunc("POST /watch", d.handleWatch)

	d.server = &http.Server{
		Addr:    d.addr,
		Handler: mux,
	}

	ln, err := net.Listen("tcp", d.addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", d.addr, err)
	}
	d.logger.Info("daemon started", "addr", d.addr, "sessions", len(d.registry))

	errCh := make(chan error, 1)
	go func() {
		if err := d.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		d.scheduler.Stop()
		d.fileWatcher.Stop()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = d.server.Shutdown(shutCtx)
		return nil
	case err := <-errCh:
		return err
	}
}

// --- Lock management ---

func (d *Daemon) lockFile() string {
	return filepath.Join(d.sessionDir, ".daemon.lock")
}

func (d *Daemon) acquireLock() error {
	d.lockPath = d.lockFile()
	if err := os.MkdirAll(filepath.Dir(d.lockPath), 0o755); err != nil {
		return err
	}
	// Try to create exclusively.
	f, err := os.OpenFile(d.lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			// Check if the PID is still alive.
			if d.isLockStale() {
				os.Remove(d.lockPath)
				f, err = os.OpenFile(d.lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
				if err != nil {
					return err
				}
			} else {
				return fmt.Errorf("lockfile exists and process is alive")
			}
		} else {
			return err
		}
	}
	fmt.Fprintf(f, "%d\n", os.Getpid())
	f.Close()
	return nil
}

func (d *Daemon) isLockStale() bool {
	data, err := os.ReadFile(d.lockFile())
	if err != nil {
		return true
	}
	pidStr := strings.TrimSpace(string(data))
	if pidStr == "" {
		return true
	}
	var pid int
	if _, err := fmt.Sscanf(pidStr, "%d", &pid); err != nil {
		return true
	}
	// Check if process exists via kill(pid, 0).
	proc, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return true
	}
	return false
}

func (d *Daemon) releaseLock() {
	if d.lockPath != "" {
		os.Remove(d.lockPath)
	}
}

// --- Session scanning ---

func (d *Daemon) scanSessions() {
	d.scanDir(d.sessionDir)
	// Also scan project session dirs.
	projectsDir := filepath.Join(filepath.Dir(d.sessionDir), "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sessDir := filepath.Join(projectsDir, e.Name(), "sessions")
		d.scanDir(sessDir)
	}
}

func (d *Daemon) scanDir(dir string) {
	pattern := filepath.Join(dir, "*.runtime.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return
	}
	for _, runtimePath := range matches {
		// Derive session path: strip .runtime.json
		sessionPath := strings.TrimSuffix(runtimePath, ".runtime.json")
		m, ok, err := agent.LoadRuntimeMeta(sessionPath)
		if err != nil || !ok {
			continue
		}
		id := agent.BranchID(sessionPath)
		d.mu.Lock()
		d.registry[id] = &SessionEntry{
			ID:           id,
			Path:         sessionPath,
			Runtime:      m,
			DiscoveredAt: time.Now(),
		}
		d.mu.Unlock()
	}
}

// recoverInterrupted marks any session with Run.Status="running" as
// "interrupted" — these were killed mid-flight. Does NOT auto-resume.
func (d *Daemon) recoverInterrupted() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, entry := range d.registry {
		if entry.Runtime.Run.Status == "running" {
			entry.Runtime.Run.Status = "interrupted"
			entry.Runtime.Run.LastError = "daemon startup recovery"
			// Persist the change.
			if err := agent.SaveRuntimeMeta(entry.Path, entry.Runtime); err != nil {
				d.logger.Warn("recover interrupted: save failed", "id", entry.ID, "err", err)
			} else {
				d.logger.Info("recovered interrupted session", "id", entry.ID)
			}
		}
	}
}
