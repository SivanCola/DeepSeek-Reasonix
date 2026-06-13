package daemon

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"reasonix/internal/agent"
)

// FileWatchConfig configures file watching for a session.
type FileWatchConfig struct {
	// Paths to watch (relative to workspace root or absolute).
	Paths []string `json:"paths"`
	// IgnorePatterns are glob patterns to skip (e.g. "node_modules/**").
	IgnorePatterns []string `json:"ignore_patterns"`
	// Debounce is how long to wait after the last change before triggering.
	Debounce time.Duration `json:"debounce"`
	// Enabled controls whether this watcher is active.
	Enabled bool `json:"enabled"`
}

// FileWatcher monitors filesystem changes and queues wakeups for sessions
// that have file watch configurations. It uses polling (not inotify) for
// simplicity and portability — the tick interval is the debounce period.
type FileWatcher struct {
	daemon *Daemon
	logger *slog.Logger

	mu       sync.Mutex
	watches  map[string]*watchState // session ID → state
	running  bool
	cancel   context.CancelFunc
}

type watchState struct {
	config    FileWatchConfig
	lastSeen  map[string]time.Time // path → last mod time
	lastFired time.Time
	pending   bool // debounce pending
	timer     time.Time
}

// DefaultIgnorePatterns are always excluded from file watching.
var DefaultIgnorePatterns = []string{
	"node_modules",
	".git",
	"__pycache__",
	".venv",
	"vendor",
	"target",
	"build",
	"dist",
	".env",
	"*.key",
	"*.pem",
	".DS_Store",
}

// NewFileWatcher creates a file watcher bound to the daemon.
func NewFileWatcher(d *Daemon, logger *slog.Logger) *FileWatcher {
	return &FileWatcher{
		daemon:  d,
		logger:  logger,
		watches: make(map[string]*watchState),
	}
}

// Register adds or updates a file watch for a session.
func (fw *FileWatcher) Register(sessionID string, cfg FileWatchConfig) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.watches[sessionID] = &watchState{
		config:   cfg,
		lastSeen: make(map[string]time.Time),
	}
}

// Unregister removes a file watch for a session.
func (fw *FileWatcher) Unregister(sessionID string) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	delete(fw.watches, sessionID)
}

// Start begins the polling loop.
func (fw *FileWatcher) Start(ctx context.Context) {
	fw.mu.Lock()
	if fw.running {
		fw.mu.Unlock()
		return
	}
	ctx, fw.cancel = context.WithCancel(ctx)
	fw.running = true
	fw.mu.Unlock()

	ticker := time.NewTicker(2 * time.Second) // poll every 2s
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fw.mu.Lock()
			fw.running = false
			fw.mu.Unlock()
			return
		case <-ticker.C:
			fw.poll()
		}
	}
}

// Stop halts the file watcher.
func (fw *FileWatcher) Stop() {
	fw.mu.Lock()
	if fw.cancel != nil {
		fw.cancel()
	}
	fw.mu.Unlock()
}

func (fw *FileWatcher) poll() {
	fw.mu.Lock()
	sessions := make([]string, 0, len(fw.watches))
	for id := range fw.watches {
		sessions = append(sessions, id)
	}
	fw.mu.Unlock()

	now := time.Now()
	for _, id := range sessions {
		fw.mu.Lock()
		state, ok := fw.watches[id]
		fw.mu.Unlock()
		if !ok {
			continue
		}
		if !state.config.Enabled {
			continue
		}

		changes := fw.detectChanges(state)
		if len(changes) == 0 {
			// Check if debounce timer has elapsed.
			if state.pending && now.After(state.timer) {
				fw.fireWakeup(id, state, now)
			}
			continue
		}

		// Changes detected — start/reset debounce timer.
		debounce := state.config.Debounce
		if debounce == 0 {
			debounce = 3 * time.Second
		}
		state.pending = true
		state.timer = now.Add(debounce)
	}
}

func (fw *FileWatcher) detectChanges(state *watchState) []string {
	var changed []string
	for _, path := range state.config.Paths {
		entries := fw.walkPath(path, state.config.IgnorePatterns)
		for _, entry := range entries {
			info, err := os.Stat(entry)
			if err != nil {
				continue
			}
			mod := info.ModTime()
			if prev, ok := state.lastSeen[entry]; ok {
				if mod.After(prev) {
					changed = append(changed, entry)
				}
			}
			state.lastSeen[entry] = mod
		}
	}
	return changed
}

func (fw *FileWatcher) walkPath(root string, ignorePatterns []string) []string {
	var files []string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if fw.shouldIgnore(name, ignorePatterns) {
				return filepath.SkipDir
			}
			return nil
		}
		if fw.shouldIgnore(info.Name(), ignorePatterns) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	return files
}

func (fw *FileWatcher) shouldIgnore(name string, patterns []string) bool {
	allPatterns := append(DefaultIgnorePatterns, patterns...)
	for _, p := range allPatterns {
		if matched, _ := filepath.Match(p, name); matched {
			return true
		}
		// Also check if the directory name matches.
		if strings.EqualFold(name, p) {
			return true
		}
	}
	return false
}

func (fw *FileWatcher) fireWakeup(sessionID string, state *watchState, now time.Time) {
	state.pending = false
	state.lastFired = now

	fw.daemon.mu.Lock()
	entry, ok := fw.daemon.registry[sessionID]
	if !ok {
		fw.daemon.mu.Unlock()
		return
	}

	// Guards: goal must be active, run must not be in-flight.
	if entry.Runtime.Goal.Status != "running" && entry.Runtime.Goal.Status != "blocked" {
		fw.daemon.mu.Unlock()
		return
	}
	if entry.Runtime.Run.Status == "running" || entry.Runtime.Run.Status == "pending_continue" {
		fw.daemon.mu.Unlock()
		return
	}

	entry.Runtime.Run.Status = "pending_continue"
	entry.Runtime.Run.LastWakeupReason = "file_change"
	entry.Runtime.Run.ResumeCount++
	entry.Runtime.Scheduler.LastWakeupAt = now
	entry.Runtime.Scheduler.LastWakeupReason = "file_change"
	runtime := entry.Runtime
	path := entry.Path
	fw.daemon.mu.Unlock()

	if err := agent.SaveRuntimeMeta(path, runtime); err != nil {
		fw.logger.Warn("file watcher: save runtime", "err", err, "session", sessionID)
	} else {
		fw.logger.Info("file watcher triggered wakeup", "session", sessionID)
	}
}
