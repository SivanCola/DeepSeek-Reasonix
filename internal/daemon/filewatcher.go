package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
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

	mu      sync.Mutex
	watches map[string]*watchState // session ID → state
	running bool
	cancel  context.CancelFunc
}

type watchState struct {
	config    FileWatchConfig
	lastSeen  map[string]time.Time // path → last mod time
	lastFired time.Time
	pending   bool // debounce pending
	changes   map[string]struct{}
	timer     time.Time
}

const maxFileWatchSummaryFiles = 20

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
		changes:  make(map[string]struct{}),
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
		for _, change := range changes {
			state.changes[change] = struct{}{}
		}
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
	changes := sortedFileWatchChanges(state.changes)
	state.changes = make(map[string]struct{})
	summary := fileWatchWakeupContext(state.config, changes)

	fw.daemon.mu.Lock()
	entry, ok := fw.daemon.registry[sessionID]
	if !ok {
		fw.daemon.mu.Unlock()
		return
	}
	if _, running := fw.daemon.activeRuns[sessionID]; running {
		fw.daemon.mu.Unlock()
		return
	}

	// Guards: goal must be active, run must not be in-flight.
	if entry.Runtime.Goal.Status != "running" && entry.Runtime.Goal.Status != "blocked" {
		fw.daemon.mu.Unlock()
		return
	}
	if agent.IsRunInFlight(entry.Runtime.Run.Status) {
		fw.daemon.mu.Unlock()
		return
	}
	wait := entry.Runtime.Wait
	if wait.Kind != "" && wait.Kind != "file" {
		runtime := entry.Runtime
		path := entry.Path
		fw.daemon.mu.Unlock()
		fw.daemon.appendTimeline(path, agent.RuntimeTimelineEvent{
			Type:       "file_change_ignored",
			Source:     "file_watch",
			Reason:     "waiting_for_" + wait.Kind,
			Step:       "deterministic",
			RunStatus:  runtime.Run.Status,
			GoalStatus: runtime.Goal.Status,
			WaitKind:   wait.Kind,
			Subject:    wait.Subject,
			Message:    summary,
		})
		return
	}
	if wait.Kind == "file" && !fileWaitMatches(wait, state.config, changes) {
		runtime := entry.Runtime
		path := entry.Path
		fw.daemon.mu.Unlock()
		fw.daemon.appendTimeline(path, agent.RuntimeTimelineEvent{
			Type:       "wait_file_ignored",
			Source:     "file_watch",
			Reason:     "changed files did not match wait condition",
			Step:       "deterministic",
			RunStatus:  runtime.Run.Status,
			GoalStatus: runtime.Goal.Status,
			WaitKind:   wait.Kind,
			Subject:    wait.Subject,
			Message:    summary,
		})
		return
	}
	if ok, reason := reserveAutoWakeupBudget(&entry.Runtime, "file_watch", now); !ok {
		entry.Runtime.Scheduler.LastWakeupAt = now
		entry.Runtime.Scheduler.LastWakeupReason = "budget_blocked:file_watch"
		runtime := entry.Runtime
		path := entry.Path
		fw.daemon.mu.Unlock()
		if err := agent.SaveRuntimeMeta(path, runtime); err != nil {
			fw.logger.Warn("file watcher: save runtime after budget block", "err", err, "session", sessionID)
		}
		fw.daemon.appendTimeline(path, agent.RuntimeTimelineEvent{
			Type:       "wakeup_budget_blocked",
			Source:     "file_watch",
			Reason:     reason,
			Step:       "deterministic",
			RunStatus:  runtime.Run.Status,
			GoalStatus: runtime.Goal.Status,
			WaitKind:   runtime.Wait.Kind,
			Subject:    runtime.Wait.Subject,
			Message:    reason,
		})
		return
	}

	clearFileWait := wait.Kind == "file"
	if clearFileWait {
		entry.Runtime.Wait = agent.RuntimeWaitMeta{}
		entry.Runtime.FileWatch = agent.RuntimeWatchMeta{}
	}
	entry.Runtime.Run.Status = agent.RunStatusQueued
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
	if clearFileWait {
		fw.Unregister(sessionID)
	}
	fw.daemon.appendTimeline(path, agent.RuntimeTimelineEvent{
		Type:       "file_change_detected",
		Source:     "file_watch",
		Reason:     "file_change",
		Step:       "deterministic",
		RunStatus:  runtime.Run.Status,
		GoalStatus: runtime.Goal.Status,
		WaitKind:   wait.Kind,
		Subject:    wait.Subject,
		Message:    summary,
	})
	fw.daemon.enqueueIntent(RunIntent{
		SessionID:   sessionID,
		SessionPath: path,
		Source:      "file_watch",
		Reason:      "file_change",
		Context:     summary,
	})
}

func fileWaitMatches(wait agent.RuntimeWaitMeta, cfg FileWatchConfig, changes []string) bool {
	if len(wait.FilePaths) == 0 {
		return true
	}
	for _, changed := range changes {
		for _, want := range wait.FilePaths {
			if filePathMatchesWait(want, changed, cfg.Paths) {
				return true
			}
		}
	}
	return false
}

func filePathMatchesWait(want, changed string, roots []string) bool {
	want = filepath.Clean(strings.TrimSpace(want))
	if want == "." || want == "" {
		return false
	}
	changed = filepath.Clean(changed)
	display := filepath.Clean(displayFileWatchPath(roots, changed))
	base := filepath.Base(changed)
	candidates := []string{changed, display, base}
	matches := func(pattern, candidate string) bool {
		candidate = filepath.Clean(candidate)
		if candidate == pattern || strings.HasSuffix(candidate, string(filepath.Separator)+pattern) {
			return true
		}
		if strings.HasPrefix(candidate, pattern+string(filepath.Separator)) {
			return true
		}
		if matched, _ := filepath.Match(pattern, candidate); matched {
			return true
		}
		if matched, _ := filepath.Match(pattern, filepath.Base(candidate)); matched {
			return true
		}
		return false
	}
	if !filepath.IsAbs(want) {
		for _, root := range roots {
			root = filepath.Clean(strings.TrimSpace(root))
			if root == "" {
				continue
			}
			if matches(filepath.Join(root, want), changed) {
				return true
			}
			if rel, err := filepath.Rel(filepath.Dir(root), changed); err == nil {
				candidates = append(candidates, filepath.Clean(rel))
			}
		}
	}
	for _, candidate := range candidates {
		if matches(want, candidate) {
			return true
		}
	}
	return false
}

func sortedFileWatchChanges(changes map[string]struct{}) []string {
	if len(changes) == 0 {
		return nil
	}
	out := make([]string, 0, len(changes))
	for path := range changes {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func fileWatchWakeupContext(cfg FileWatchConfig, changes []string) string {
	if len(changes) == 0 {
		return "File watch detected changes, but no changed file paths were captured."
	}
	limit := len(changes)
	if limit > maxFileWatchSummaryFiles {
		limit = maxFileWatchSummaryFiles
	}
	var b strings.Builder
	fmt.Fprintf(&b, "File watch detected %d changed file(s).\nChanged files:", len(changes))
	for _, path := range changes[:limit] {
		fmt.Fprintf(&b, "\n- %s", displayFileWatchPath(cfg.Paths, path))
	}
	if omitted := len(changes) - limit; omitted > 0 {
		fmt.Fprintf(&b, "\n... %d more file(s) omitted", omitted)
	}
	return b.String()
}

func displayFileWatchPath(roots []string, path string) string {
	path = filepath.Clean(path)
	best := path
	bestLen := -1
	for _, root := range roots {
		root = filepath.Clean(strings.TrimSpace(root))
		if root == "" {
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
			continue
		}
		if rel == "." {
			rel = filepath.Base(path)
		}
		if len(root) > bestLen {
			best = rel
			bestLen = len(root)
		}
	}
	return best
}
