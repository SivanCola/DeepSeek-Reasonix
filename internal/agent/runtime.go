package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"reasonix/internal/fileutil"
)

// RuntimeMeta is the dynamic sidecar record that tracks the agent's runtime
// state — active goal, run status, and scheduler hints. It lives beside the
// session file at <session>.runtime.json and is the authoritative source for
// resumable state (desktop tab profiles are UI-only).
//
// The sidecar is independent of BranchMeta: branch meta is structural (tree
// lineage, topic), runtime meta is ephemeral execution state.
type RuntimeMeta struct {
	Version   int              `json:"version"`
	SessionID string           `json:"session_id"`
	UpdatedAt time.Time        `json:"updated_at"`
	Goal      RuntimeGoalMeta  `json:"goal,omitempty"`
	Run       RuntimeRunMeta   `json:"run,omitempty"`
	Scheduler RuntimeSchedMeta `json:"scheduler,omitempty"`
}

// RuntimeGoalMeta captures the active goal's lifecycle.
type RuntimeGoalMeta struct {
	Text        string    `json:"text,omitempty"`
	Status      string    `json:"status,omitempty"` // running|complete|blocked|stopped
	Turns       int       `json:"turns,omitempty"`
	BlockCount  int       `json:"block_count,omitempty"`
	BlockReason string    `json:"block_reason,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

// RuntimeRunMeta captures the run loop's lifecycle.
type RuntimeRunMeta struct {
	Status           string    `json:"status,omitempty"` // idle|running|interrupted|failed|stopped
	LastTurnAt       time.Time `json:"last_turn_at,omitempty"`
	LastError        string    `json:"last_error,omitempty"`
	ResumeCount      int       `json:"resume_count,omitempty"`
	LastWakeupReason string    `json:"last_wakeup_reason,omitempty"`
}

// RuntimeSchedMeta holds scheduler/wakeup state for future cron/webhook use.
type RuntimeSchedMeta struct {
	// Schedule configuration (set by user via /goal schedule or API).
	DailyAt  string        `json:"daily_at,omitempty"`  // "HH:MM" in local time, e.g. "09:00"
	Interval time.Duration `json:"interval,omitempty"`  // fixed interval between wakeups, e.g. 1h
	Enabled  bool          `json:"enabled,omitempty"`   // whether scheduling is active

	// Runtime state (managed by the scheduler).
	NextWakeupAt      time.Time `json:"next_wakeup_at,omitempty"`
	LastWakeupAt      time.Time `json:"last_wakeup_at,omitempty"`
	LastWakeupReason  string    `json:"last_wakeup_reason,omitempty"`
	LastWakeupEventID string    `json:"last_wakeup_event_id,omitempty"`
}

const runtimeMetaVersion = 1

// RuntimeMetaPath returns the path to the runtime sidecar for a session file.
func RuntimeMetaPath(sessionPath string) string {
	if sessionPath == "" {
		return ""
	}
	return sessionPath + ".runtime.json"
}

// LoadRuntimeMeta reads the runtime sidecar. Returns (meta, true, nil) on
// success, (zero, false, nil) if the file does not exist, and (zero, false, err)
// on read/decode failure. A corrupt file is an error — callers decide whether to
// treat it as fatal or advisory.
func LoadRuntimeMeta(sessionPath string) (RuntimeMeta, bool, error) {
	path := RuntimeMetaPath(sessionPath)
	if path == "" {
		return RuntimeMeta{}, false, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return RuntimeMeta{}, false, nil
		}
		return RuntimeMeta{}, false, err
	}
	var m RuntimeMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return RuntimeMeta{}, false, fmt.Errorf("decode runtime meta %s: %w", path, err)
	}
	return m, true, nil
}

// SaveRuntimeMeta atomically writes the runtime sidecar to disk. It stamps
// Version and UpdatedAt automatically.
func SaveRuntimeMeta(sessionPath string, m RuntimeMeta) error {
	path := RuntimeMetaPath(sessionPath)
	if path == "" {
		return fmt.Errorf("empty session path")
	}
	m.Version = runtimeMetaVersion
	m.UpdatedAt = time.Now().UTC()
	if m.SessionID == "" {
		m.SessionID = BranchID(sessionPath)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".runtime.*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return fileutil.ReplaceFile(tmpPath, path)
}

// RemoveRuntimeMeta deletes the runtime sidecar if it exists.
func RemoveRuntimeMeta(sessionPath string) error {
	path := RuntimeMetaPath(sessionPath)
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
