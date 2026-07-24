package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/fileutil"
)

const (
	windowRestoreStateVersion = 1
	windowRestoreTimeout      = 12 * time.Second
)

type windowRestoreState struct {
	SchemaVersion   int    `json:"schemaVersion"`
	PID             int    `json:"pid"`
	Source          string `json:"source"`
	StartedAt       string `json:"startedAt"`
	TimeoutReported bool   `json:"timeoutReported,omitempty"`
}

var windowRestoreMu sync.Mutex

func windowRestoreStatePath() string {
	return filepath.Join(config.MemoryUserDir(), "repair", "window-restore-state.json")
}

func writeWindowRestoreState(state windowRestoreState) bool {
	body, err := json.Marshal(state)
	if err != nil {
		return false
	}
	return fileutil.AtomicWriteFile(windowRestoreStatePath(), body, 0o600) == nil
}

func readWindowRestoreState() (windowRestoreState, error) {
	body, err := os.ReadFile(windowRestoreStatePath())
	if err != nil {
		return windowRestoreState{}, err
	}
	var state windowRestoreState
	if err := json.Unmarshal(body, &state); err != nil {
		return windowRestoreState{}, err
	}
	return state, nil
}

func (a *App) observeIncompleteWindowRestore() {
	if !windowRestoreDiagnosticsSupported() {
		return
	}
	state, err := readWindowRestoreState()
	if err != nil {
		return
	}
	if state.PID > 0 && windowRestoreOwnerAlive(state.PID) {
		return
	}
	_ = os.Remove(windowRestoreStatePath())
	if state.TimeoutReported {
		return
	}
	_ = writePendingReport(windowRestoreFailureReport("incomplete", state.Source, state.StartedAt), true)
	a.recordDiagnosticMetric("desktop_restore", "incomplete")
}

func (a *App) showMainWindowFrom(source string) {
	if a.ctx == nil {
		return
	}
	if !windowRestoreDiagnosticsSupported() {
		showFromBackground(a.ctx, a.backgroundMaximised.Swap(false))
		a.kickDeferredRebuildRetry()
		return
	}

	windowRestoreMu.Lock()
	defer windowRestoreMu.Unlock()
	state := windowRestoreState{
		SchemaVersion: windowRestoreStateVersion,
		PID:           os.Getpid(),
		Source:        metricBucket(source),
		StartedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	_ = writeWindowRestoreState(state)
	done := make(chan struct{})
	watcherDone := make(chan struct{})
	var timedOut atomic.Bool
	go func() {
		defer close(watcherDone)
		timer := time.NewTimer(windowRestoreTimeout)
		defer timer.Stop()
		select {
		case <-done:
			return
		case <-timer.C:
			timedOut.Store(true)
			if writePendingReport(windowRestoreFailureReport("timeout", state.Source, state.StartedAt), true) {
				state.TimeoutReported = true
				_ = writeWindowRestoreState(state)
			}
			a.recordDiagnosticMetric("desktop_restore", "timeout")
		}
	}()

	showFromBackground(a.ctx, a.backgroundMaximised.Swap(false))
	close(done)
	<-watcherDone
	_ = os.Remove(windowRestoreStatePath())
	if !timedOut.Load() {
		a.recordDiagnosticMetric("desktop_restore", "success")
	}
	a.kickDeferredRebuildRetry()
}

func windowRestoreFailureReport(kind, source, startedAt string) crashReport {
	kind = metricBucket(kind)
	source = metricBucket(source)
	report := baseCrashReport("performance")
	report.SchemaVersion = 2
	report.Source = "native.window"
	report.Label = "windows.window_restore." + kind
	report.ErrorType = "WindowsWindowRestoreFailure"
	report.ErrorMessage = sanitizeCrashText("Windows window restoration did not complete normally.", maxCrashFieldBytes)
	report.TopFrame = "windows.window_restore." + source
	report.FingerprintHint = "windows.window_restore." + kind + "." + source
	report.OccurredAt = time.Now().UTC().Format(time.RFC3339)
	report.Message = sanitizeCrashText(fmt.Sprintf(`[windows.window_restore.%s]

Reasonix could not confirm that the hidden window was restored.

source: %s
attempt started at: %s
timeout: %s`, kind, source, sanitizeCrashField(startedAt, 64), windowRestoreTimeout), maxCrashDetailBytes)
	return report
}
