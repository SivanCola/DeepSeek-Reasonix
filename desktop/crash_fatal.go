package main

import (
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"reasonix/internal/config"
)

const (
	fatalCrashFile        = "crash-fatal.log"
	fatalCrashCoveredFile = "crash-fatal-covered"
)

func fatalCrashPath() string {
	return filepath.Join(config.MemoryUserDir(), fatalCrashFile)
}

func fatalCrashCoveredPath() string {
	return filepath.Join(config.MemoryUserDir(), fatalCrashCoveredFile)
}

func markFatalCrashCovered() {
	_ = os.WriteFile(fatalCrashCoveredPath(), []byte("structured\n"), 0o600)
}

// capturePreviousFatalCrash converts runtime.SetCrashOutput's previous-process
// dump into the normal scrubbed queue before a new dump file is installed.
func capturePreviousFatalCrash() {
	path := fatalCrashPath()
	if _, err := os.Stat(fatalCrashCoveredPath()); err == nil {
		_ = os.Remove(fatalCrashCoveredPath())
		_ = os.Remove(path)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	occurredAt := time.Now().UTC()
	if info, statErr := f.Stat(); statErr == nil {
		occurredAt = info.ModTime().UTC()
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, maxCrashStackBytes+1))
	_ = f.Close()
	if readErr != nil || len(strings.TrimSpace(string(raw))) == 0 {
		_ = os.Remove(path)
		return
	}
	stack := sanitizeFatalRuntimeDump(string(raw))
	report := baseCrashReport("crash")
	report.SchemaVersion = 2
	report.Source = "go.runtime"
	report.Label = "go.fatal"
	report.ErrorType = "GoRuntimeFatal"
	report.ErrorMessage = "Go runtime terminated the desktop process."
	report.Stack = stack
	report.TopFrame = topFrameFromStack(stack)
	report.FingerprintHint = "go.runtime.fatal"
	report.OccurredAt = occurredAt.Format(time.RFC3339)
	report.Message = sanitizeCrashText("[go.runtime.fatal]\n\n"+stack, maxCrashDetailBytes)
	if writePendingReport(report, true) {
		_ = os.Remove(path)
	}
}

// sanitizeFatalRuntimeDump removes panic values and preamble text that could
// originate in user-controlled errors, while retaining runtime classification
// and symbolized goroutine stacks for diagnosis.
func sanitizeFatalRuntimeDump(raw string) string {
	lines := strings.Split(raw, "\n")
	classification := "runtime crash output"
	stackStart := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "fatal error:"):
			classification = sanitizeCrashText(trimmed, 256)
		case strings.HasPrefix(trimmed, "panic:"):
			classification = "panic: [redacted panic value]"
		}
		if strings.HasPrefix(trimmed, "goroutine ") {
			stackStart = i
			break
		}
	}
	stack := ""
	if stackStart >= 0 {
		stack = strings.Join(lines[stackStart:], "\n")
	}
	return sanitizeCrashText(classification+"\n\n"+stack, maxCrashStackBytes)
}

// installFatalCrashOutput asks the Go runtime to mirror unrecovered panics and
// fatal runtime errors to a durable file. The runtime duplicates the descriptor,
// so the file may be closed after SetCrashOutput returns.
func installFatalCrashOutput() {
	path := fatalCrashPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return
	}
	_ = f.Close()
}
