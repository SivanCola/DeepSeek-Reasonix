package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type managerRunRecorder struct {
	path string
	mu   sync.Mutex
	err  error
}

type managerRunEvent struct {
	Time  time.Time `json:"time"`
	Event string    `json:"event"`
	Data  any       `json:"data,omitempty"`
}

func newManagerRunRecorder(dir, runID string) (*managerRunRecorder, error) {
	if dir == "" {
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	name := fmt.Sprintf("%s-%s.jsonl", time.Now().UTC().Format("20060102-150405.000"), safeID(runID))
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return &managerRunRecorder{path: path}, nil
}

func (r *managerRunRecorder) Path() string {
	if r == nil {
		return ""
	}
	return r.path
}

func (r *managerRunRecorder) Record(event string, data any) {
	if r == nil || r.path == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	f, err := os.OpenFile(r.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		r.err = err
		return
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(managerRunEvent{Time: time.Now().UTC(), Event: event, Data: data}); err != nil {
		r.err = err
	}
}

func (r *managerRunRecorder) Err() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

type managerRunAgentSpec struct {
	ID          string   `json:"id"`
	Description string   `json:"description,omitempty"`
	Branch      string   `json:"branch,omitempty"`
	Worktree    string   `json:"worktree,omitempty"`
	Isolation   string   `json:"isolation"`
	Mode        string   `json:"mode"`
	DependsOn   []string `json:"depends_on,omitempty"`
}

type managerRunStart struct {
	Repo          string                `json:"repo,omitempty"`
	Concurrency   int                   `json:"concurrency"`
	Isolation     string                `json:"isolation"`
	Mode          string                `json:"mode"`
	BaseRef       string                `json:"base_ref,omitempty"`
	MergeStrategy string                `json:"merge_strategy"`
	Cleanup       string                `json:"cleanup"`
	Agents        []managerRunAgentSpec `json:"agents"`
}

func managerRunStartRecord(repo string, p agentsPayload) managerRunStart {
	agents := make([]managerRunAgentSpec, len(p.Agents))
	for i, a := range p.Agents {
		agents[i] = managerRunAgentSpec{
			ID: a.ID, Description: a.Description, Branch: a.Branch,
			Worktree: a.WorktreeDir, Isolation: a.Isolation, Mode: a.Mode,
			DependsOn: append([]string(nil), a.DependsOn...),
		}
	}
	return managerRunStart{
		Repo: repo, Concurrency: p.Concurrency, Isolation: p.Isolation,
		Mode: p.Mode, BaseRef: p.BaseRef, MergeStrategy: p.MergeStrategy,
		Cleanup: p.Cleanup, Agents: agents,
	}
}

type managerRunResult struct {
	ID             string   `json:"id"`
	Agent          string   `json:"agent"`
	Branch         string   `json:"branch,omitempty"`
	Worktree       string   `json:"worktree,omitempty"`
	PromptBytes    int      `json:"prompt_bytes,omitempty"`
	PromptRedacted bool     `json:"prompt_redacted,omitempty"`
	Commits        []string `json:"commits,omitempty"`
	Status         string   `json:"status"`
	Next           string   `json:"next,omitempty"`
	Error          string   `json:"error,omitempty"`
	Isolation      string   `json:"isolation"`
	Mode           string   `json:"mode"`
}

func managerRunResultRecord(r result) managerRunResult {
	out := managerRunResult{
		ID: r.id, Agent: r.label, Branch: r.branch, Worktree: r.worktree,
		PromptBytes: len(r.task), PromptRedacted: r.task != "",
		Commits: append([]string(nil), r.commits...),
		Status:  r.status, Next: r.next, Isolation: r.isolation, Mode: r.mode,
	}
	if r.err != nil {
		out.Error = r.err.Error()
	}
	return out
}

type managerRunActions struct {
	Merged   []string `json:"merged,omitempty"`
	Cleaned  []string `json:"cleaned,omitempty"`
	Deleted  []string `json:"deleted,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Error    string   `json:"error,omitempty"`
}

func managerRunActionsRecord(a managerActions) managerRunActions {
	out := managerRunActions{
		Merged:   append([]string(nil), a.merged...),
		Cleaned:  append([]string(nil), a.cleaned...),
		Deleted:  append([]string(nil), a.deleted...),
		Warnings: append([]string(nil), a.warnings...),
	}
	if a.err != nil {
		out.Error = a.err.Error()
	}
	return out
}
