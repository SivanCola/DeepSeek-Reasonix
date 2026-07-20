package recovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/agent"
	fileencoding "reasonix/internal/fileutil/encoding"
)

// PathFor returns the recovery state sidecar for a main session path.
// Example: session.jsonl → session.recovery.json
func PathFor(sessionPath string) string {
	if strings.TrimSpace(sessionPath) == "" {
		return ""
	}
	return strings.TrimSuffix(sessionPath, ".jsonl") + ".recovery.json"
}

// ReviewerPathFor returns the recovery reviewer transcript path.
// Example: session.jsonl → session.recovery-reviewer.jsonl
func ReviewerPathFor(sessionPath string) string {
	if strings.TrimSpace(sessionPath) == "" {
		return ""
	}
	return strings.TrimSuffix(sessionPath, ".jsonl") + ".recovery-reviewer.jsonl"
}

// SaveSnapshot writes the recovery gate state beside the session file.
func SaveSnapshot(sessionPath string, snap Snapshot) error {
	path := PathFor(sessionPath)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadSnapshot reads a previously saved recovery gate state.
// Missing files return an empty snapshot and nil error.
func LoadSnapshot(sessionPath string) (Snapshot, error) {
	path := PathFor(sessionPath)
	if path == "" {
		return Snapshot{}, nil
	}
	data, err := fileencoding.ReadFileUTF8(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Snapshot{}, nil
		}
		return Snapshot{}, err
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return Snapshot{}, err
	}
	if snap.Tasks == nil {
		snap.Tasks = map[string]*TaskState{}
	}
	return snap, nil
}

// Save implements reviewer transcript persistence for cache warmth.
func (s *Session) Save(path string) error {
	if s == nil || s.sess == nil || strings.TrimSpace(path) == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return s.sess.Save(path)
}

// Load restores a previously saved reviewer transcript when its system prompt
// still matches the fixed recovery policy (otherwise starts fresh).
func (s *Session) Load(path string) error {
	if s == nil || strings.TrimSpace(path) == "" {
		return nil
	}
	sess, err := agent.LoadSession(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	msgs := sess.Snapshot()
	if len(msgs) == 0 || string(msgs[0].Role) != "system" || strings.TrimSpace(msgs[0].Content) != PolicyPrompt {
		// Policy changed or corrupt prefix — drop cache, keep deterministic policy.
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agent.SetSession(sess)
	s.sess = sess
	return nil
}
