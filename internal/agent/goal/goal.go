package goal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Status tracks where a goal is in its lifecycle.
type Status string

const (
	StatusDraft     Status = "draft"
	StatusActive    Status = "active"
	StatusVerifying Status = "verifying"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

// State is the persistent goal state stored on disk.
type State struct {
	Goal        string   `json:"goal"`
	Status      Status   `json:"status"`
	Context     string   `json:"context"`
	Checkpoints []string `json:"checkpoints"`
	Attempts    int      `json:"attempts"`
	Result      string   `json:"result"`
}

// Dir returns the goal state directory for a workspace.
func Dir(workspace string) string {
	return filepath.Join(workspace, ".reasonix", "goal")
}

// Load reads the goal state from disk. Returns nil if no state exists.
func Load(dir string) (*State, error) {
	p := filepath.Join(dir, "state.json")
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parse goal state: %w", err)
	}
	return &s, nil
}

// Save writes the goal state to disk.
func (s *State) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "state.json"), b, 0o644)
}

// Delete removes the goal state directory.
func Delete(dir string) error {
	return os.RemoveAll(dir)
}

// Prompt builds the system prompt injection for the current goal state.
func (s *State) Prompt() string {
	switch s.Status {
	case StatusDraft:
		return fmt.Sprintf("## Goal\n\nYou are working toward this goal:\n\n%s\n\nAnalyze the goal and propose a concrete plan.", s.Goal)
	case StatusActive:
		prompt := fmt.Sprintf("## Goal (attempt %d)\n\nGoal: %s", s.Attempts, s.Goal)
		if s.Context != "" {
			prompt += fmt.Sprintf("\n\nPrevious context: %s", s.Context)
		}
		prompt += "\n\nContinue working toward the goal. When you believe the goal is complete, verify by running the appropriate tests."
		return prompt
	case StatusCompleted:
		return fmt.Sprintf("## Goal Completed\n\nGoal: %s\n\nResult: %s", s.Goal, s.Result)
	default:
		return fmt.Sprintf("## Goal\n\nGoal: %s\n\nStatus: %s", s.Goal, s.Status)
	}
}
