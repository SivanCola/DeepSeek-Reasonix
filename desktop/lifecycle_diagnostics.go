package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/fileutil"
	"reasonix/internal/repair"
)

const (
	desktopLifecycleSchemaVersion = 2
	desktopLifecycleRetention     = 30 * 24 * time.Hour
	maxDesktopLifecycleRecords    = 20
)

type desktopLifecycleState struct {
	SchemaVersion int    `json:"schemaVersion"`
	PID           int    `json:"pid"`
	RunID         string `json:"runId"`
	Version       string `json:"version,omitempty"`
	Channel       string `json:"channel,omitempty"`
	Phase         string `json:"phase"`
	StartedAt     string `json:"startedAt"`
	UpdatedAt     string `json:"updatedAt"`
}

type desktopLifecycleObservation struct {
	Version   string
	Channel   string
	Phase     string
	StartedAt string
	UpdatedAt string
}

type desktopLifecycleRuntime struct {
	previousRun  repair.PreviousRunObservation
	previousRuns []desktopLifecycleObservation
	tracker      *desktopLifecycleTracker
}

type desktopLifecycleTracker struct {
	mu           sync.Mutex
	dir          string
	path         string
	state        desktopLifecycleState
	now          func() time.Time
	processAlive func(int) bool
}

func newDesktopLifecycleTracker(root, appVersion, appChannel string) *desktopLifecycleTracker {
	now := time.Now().UTC()
	runID := newDesktopLifecycleRunID()
	dir := filepath.Join(root, "diagnostics", "lifecycle")
	return &desktopLifecycleTracker{
		dir:  dir,
		path: filepath.Join(dir, strconv.Itoa(os.Getpid())+"-"+runID+".json"),
		state: desktopLifecycleState{
			SchemaVersion: desktopLifecycleSchemaVersion,
			PID:           os.Getpid(),
			RunID:         runID,
			Version:       appVersion,
			Channel:       appChannel,
			Phase:         "starting",
			StartedAt:     now.Format(time.RFC3339Nano),
			UpdatedAt:     now.Format(time.RFC3339Nano),
		},
		now:          func() time.Time { return time.Now().UTC() },
		processAlive: desktopProcessAlive,
	}
}

func initializeLifecycleDiagnostics(app *App) {
	app.lifecycle.previousRun = repair.NewStartupTracker("").ObservePreviousRun()
	cfg, err := config.Load()
	if err != nil || version == "dev" {
		return
	}
	tracker := newDesktopLifecycleTracker(config.MemoryUserDir(), version, channel)
	enabled := cfg.DesktopTelemetry()
	app.lifecycle.previousRuns = tracker.consumePrevious(enabled)
	if enabled && tracker.start() == nil {
		app.lifecycle.tracker = tracker
		installWebView2ProcessObserver(app)
	}
}

func (a *App) markDesktopHealthy() {
	a.startupReady.Store(true)
	a.lifecycle.tracker.mark("healthy")
}

func newDesktopLifecycleRunID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return strconv.FormatInt(time.Now().UTC().UnixNano(), 16)
}

func (t *desktopLifecycleTracker) start() error {
	if t == nil || t.path == "" {
		return nil
	}
	return t.writeState()
}

func (t *desktopLifecycleTracker) mark(phase string) {
	if t == nil || t.path == "" || strings.TrimSpace(phase) == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state.Phase = phase
	t.state.UpdatedAt = t.now().Format(time.RFC3339Nano)
	_ = t.writeStateLocked()
}

func (t *desktopLifecycleTracker) clean() {
	if t == nil || t.path == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	_ = os.Remove(t.path)
}

func (t *desktopLifecycleTracker) writeState() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.writeStateLocked()
}

func (t *desktopLifecycleTracker) writeStateLocked() error {
	body, err := json.Marshal(t.state)
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(t.path, body, 0o600)
}

// consumePrevious atomically owns every dead per-process record before
// returning it. When emit is false (telemetry opt-out), records are consumed
// without exposing their contents to the reporting path.
func (t *desktopLifecycleTracker) consumePrevious(emit bool) []desktopLifecycleObservation {
	if t == nil || t.dir == "" {
		return nil
	}
	entries, err := os.ReadDir(t.dir)
	if err != nil {
		return nil
	}
	now := t.now()
	observations := make([]desktopLifecycleObservation, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(t.dir, entry.Name())
		state, readErr := readDesktopLifecycleState(path)
		if readErr != nil || state.PID <= 0 || state.Phase == "" {
			if info, statErr := entry.Info(); statErr == nil && now.Sub(info.ModTime()) > desktopLifecycleRetention {
				_ = os.Remove(path)
			}
			continue
		}
		if t.processAlive(state.PID) {
			continue
		}
		claimed := path + ".claimed-" + t.state.RunID
		if err := os.Rename(path, claimed); err != nil {
			continue
		}
		state, readErr = readDesktopLifecycleState(claimed)
		_ = os.Remove(claimed)
		if readErr != nil || !emit {
			continue
		}
		observations = append(observations, desktopLifecycleObservation{
			Version: state.Version, Channel: state.Channel, Phase: state.Phase,
			StartedAt: state.StartedAt, UpdatedAt: state.UpdatedAt,
		})
	}
	t.pruneRecords()
	return observations
}

func readDesktopLifecycleState(path string) (desktopLifecycleState, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return desktopLifecycleState{}, err
	}
	var state desktopLifecycleState
	if err := json.Unmarshal(body, &state); err != nil {
		return desktopLifecycleState{}, err
	}
	return state, nil
}

func (t *desktopLifecycleTracker) pruneRecords() {
	entries, err := os.ReadDir(t.dir)
	if err != nil || len(entries) <= maxDesktopLifecycleRecords {
		return
	}
	type candidate struct {
		path string
		at   time.Time
	}
	candidates := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(t.dir, entry.Name())
		state, err := readDesktopLifecycleState(path)
		if err != nil || t.processAlive(state.PID) {
			continue
		}
		if info, err := entry.Info(); err == nil {
			candidates = append(candidates, candidate{path: path, at: info.ModTime()})
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].at.Before(candidates[j].at) })
	for len(candidates) > maxDesktopLifecycleRecords {
		_ = os.Remove(candidates[0].path)
		candidates = candidates[1:]
	}
}
