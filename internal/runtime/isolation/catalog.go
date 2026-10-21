package isolation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"reasonix/internal/base/filelock"
	"reasonix/internal/contract/tool"
	"reasonix/internal/platform/worktree"
)

type ResultSummary struct {
	ID              string            `json:"id"`
	Kind            string            `json:"kind"`
	ResultID        string            `json:"resultId,omitempty"`
	Owner           string            `json:"owner,omitempty"`
	Generation      string            `json:"generation,omitempty"`
	State           State             `json:"state"`
	Activity        string            `json:"activity"`
	CandidateRoot   string            `json:"candidateRoot,omitempty"`
	BaselineTree    string            `json:"baselineTree,omitempty"`
	Tree            string            `json:"tree,omitempty"`
	Changes         []worktree.Change `json:"changes"`
	Verification    string            `json:"verification"`
	CleanupComplete bool              `json:"cleanupComplete"`
	Reason          string            `json:"reason,omitempty"`
	Actions         []string          `json:"actions"`
}

type CatalogDiagnostic struct {
	Record string `json:"record"`
	Reason string `json:"reason"`
}

type Catalog struct {
	Enabled          bool                `json:"enabled"`
	Results          []ResultSummary     `json:"results"`
	Diagnostics      []CatalogDiagnostic `json:"diagnostics"`
	RecoveryRequired bool                `json:"recoveryRequired"`
	RecoveryReason   string              `json:"recoveryReason,omitempty"`
	Recovery         *RecoveryTarget     `json:"recovery,omitempty"`
	RootScene        *RootSceneSummary   `json:"rootScene,omitempty"`
}

type RootSceneSummary struct {
	EnvironmentID   string          `json:"environmentId"`
	Generation      string          `json:"generation"`
	Runtime         string          `json:"runtime"`
	Owner           string          `json:"owner"`
	SourceWorkspace string          `json:"sourceWorkspace"`
	LiveWorkspace   string          `json:"liveWorkspace"`
	PublicationID   string          `json:"publicationId"`
	ResultID        string          `json:"resultId"`
	State           string          `json:"state"`
	Verification    string          `json:"verification"`
	Actions         []string        `json:"actions"`
	Recovery        *RecoveryTarget `json:"recovery,omitempty"`
}

type RootSceneRequest struct {
	EnvironmentID string `json:"environmentId"`
	Generation    string `json:"generation"`
	Runtime       string `json:"runtime"`
	Owner         string `json:"owner"`
	PublicationID string `json:"publicationId"`
	Action        string `json:"action"`
	Revision      string `json:"revision,omitempty"`
}

type RecoveryTarget struct {
	Workspace string   `json:"workspace"`
	Runtime   string   `json:"runtime"`
	Revision  string   `json:"revision"`
	Paths     []string `json:"paths"`
}

type RecoveryRequest struct {
	Workspace string `json:"workspace"`
	Runtime   string `json:"runtime"`
	Revision  string `json:"revision"`
}

func (t RecoveryTarget) Request() RecoveryRequest {
	return RecoveryRequest{Workspace: t.Workspace, Runtime: t.Runtime, Revision: t.Revision}
}

// Catalog reads retained decisions without pruning, rewriting or materializing
// candidates. Activity is a momentary observation, never authority for an action.
func (s *Store) Catalog(ctx context.Context) (Catalog, error) {
	out := Catalog{Enabled: s != nil, Results: []ResultSummary{}, Diagnostics: []CatalogDiagnostic{}}
	if s == nil {
		return out, nil
	}
	if s.repo.Valid() {
		if err := worktree.CheckApplyRecovery(s.repo); err != nil {
			out.RecoveryRequired, out.RecoveryReason = true, err.Error()
		}
		if preview, err := s.PreviewRecovery(ctx); err != nil {
			out.RecoveryRequired, out.RecoveryReason = true, err.Error()
		} else if preview != nil {
			out.RecoveryRequired = true
			out.RecoveryReason = "interrupted publication records require recovery; candidate results are retained"
		}
	}
	markers := s.resultMarkers()
	seen := map[string]int{}
	for _, marker := range markers {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		raw, err := os.ReadFile(marker)
		var d durableEntry
		if err == nil {
			err = json.Unmarshal(raw, &d)
		}
		if err != nil || d.Workspace == "" || !validID(d.ID) {
			out.Diagnostics = append(out.Diagnostics, CatalogDiagnostic{Record: filepath.Join(filepath.Base(filepath.Dir(marker)), filepath.Base(marker)), Reason: "unreadable or unidentified isolation record retained"})
			continue
		}
		if !sameDir(d.Workspace, s.workspace) {
			continue
		}
		if position, ok := seen[d.ID]; ok {
			out.Results[position].Reason = "duplicate isolation records retained; resolve the record identity before continuing"
			out.Results[position].Actions = []string{}
			continue
		}
		row := ResultSummary{ID: d.ID, Kind: "result", Owner: d.ResumeOwner, Generation: d.RunGeneration, State: d.State,
			CandidateRoot: d.Cand.WorktreeRoot, BaselineTree: d.Snap.Tree, Tree: d.Tree, Changes: append([]worktree.Change{}, d.Changes...),
			Verification: "unknown", CleanupComplete: d.CleanupComplete, Activity: "unknown", Actions: []string{}}
		if !supportedResultVersion(d) || !supportedLifecycle(d) {
			row.Reason = "unsupported isolation state retained; a compatible reader is required"
		} else if d.Version != 0 {
			row.Activity, row.Reason = s.resultActivity(d.ID)
		}
		if supportedResultVersion(d) && supportedLifecycle(d) && row.Activity != "active" && row.State != Applying && row.State != Integrated && !row.CleanupComplete {
			if err := s.requireSettledChildren(d.entry()); err != nil {
				row.Reason = err.Error()
			} else {
				row.Actions = []string{"discard_preview"}
			}
		}
		seen[d.ID] = len(out.Results)
		out.Results = append(out.Results, row)
	}
	out.Results = append(out.Results, s.integrationSummaries()...)
	slices.SortFunc(out.Results, func(a, b ResultSummary) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func (s *Store) resultActivity(id string) (string, string) {
	release, err := filelock.TryAcquire(filepath.Join(s.managedRoot, "."+id+".lock"))
	if errors.Is(err, filelock.ErrHeld) {
		return "unknown", "lifecycle transition in progress"
	}
	if err != nil {
		return "unknown", err.Error()
	}
	defer release()
	releaseRun, err := filelock.TryAcquire(s.runLock(id))
	if errors.Is(err, filelock.ErrHeld) {
		return "active", ""
	}
	if err != nil {
		return "unknown", err.Error()
	}
	releaseRun()
	return "inactive", ""
}

func (s *Store) integrationSummaries() []ResultSummary {
	rows := []ResultSummary{}
	paths, _ := filepath.Glob(filepath.Join(s.integrationRoot(), "int_*.json"))
	for _, path := range paths {
		id := strings.TrimSuffix(filepath.Base(path), ".json")
		attempt, _, err := s.readIntegration(id)
		if attempt.Source.RepoRoot != "" && !sameDir(attempt.Source.RepoRoot, s.repo.WorkTree) {
			continue
		}
		if err == nil {
			_, scopeErr := s.managedResultMetadata(attempt.ResultID)
			var refusal tool.Refusal
			if errors.As(scopeErr, &refusal) && refusal.Code == CodeOwnerMismatch {
				continue
			}
			if scopeErr != nil {
				err = scopeErr
			}
		}
		row := ResultSummary{ID: id, Kind: "integration", Activity: "unknown", Verification: "unknown", Changes: []worktree.Change{}, Actions: []string{}}
		if err != nil {
			row.State, row.Reason = RecoveryRequired, err.Error()
		} else {
			row.ResultID, row.Owner, row.State = attempt.ResultID, attempt.Owner, attempt.State
			row.CandidateRoot, row.BaselineTree, row.Tree = attempt.Candidate.WorktreeRoot, attempt.Source.Tree, attempt.Tree
			row.Changes, row.CleanupComplete, row.Reason = append(row.Changes, attempt.Changes...), attempt.CleanupComplete, attempt.Reason
			if attempt.Verification == "invalid" {
				row.Verification = "invalid"
			}
			if s.mutations != nil && row.State != Applying && row.State != Integrated && !row.CleanupComplete {
				row.Actions = []string{"discard_preview"}
			}
		}
		rows = append(rows, row)
	}
	return rows
}
