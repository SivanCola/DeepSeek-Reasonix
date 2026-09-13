package control

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	goaldomain "reasonix/internal/goal"
	"reasonix/internal/sessionv3"
)

// GoalDiagnosticMetadata is supplied by the host so a user-exported artifact
// identifies the exact build and negotiated feature surface that produced it.
type GoalDiagnosticMetadata struct {
	ApplicationVersion string   `json:"applicationVersion,omitempty"`
	BuildCommit        string   `json:"buildCommit,omitempty"`
	ProtocolVersion    int      `json:"protocolVersion,omitempty"`
	Capabilities       []string `json:"capabilities"`
}

type goalDiagnosticExport struct {
	SchemaVersion     int                        `json:"schemaVersion"`
	ExportedAt        time.Time                  `json:"exportedAt"`
	Metadata          GoalDiagnosticMetadata     `json:"metadata"`
	Runtime           sessionv3.RuntimeSnapshot  `json:"runtime"`
	Observation       any                        `json:"observation"`
	AcceptedThrough   uint64                     `json:"acceptedThrough"`
	DurableThrough    uint64                     `json:"durableThrough"`
	Commits           []sessionv3.Commit         `json:"commits"`
	ActivationChanges []goalDiagnosticTransition `json:"activationChanges"`
	Unavailable       []string                   `json:"unavailable"`
}

type goalDiagnosticTransition struct {
	Sequence      uint64                `json:"sequence"`
	OperationID   string                `json:"operationId"`
	GoalID        string                `json:"goalId,omitempty"`
	Revision      uint64                `json:"revision,omitempty"`
	Phase         goaldomain.Phase      `json:"phase,omitempty"`
	Activation    goaldomain.Activation `json:"activation"`
	RoundsStarted uint64                `json:"roundsStarted,omitempty"`
}

// ExportGoalDiagnostics reads the authoritative v3 event log after a Flush
// checkpoint. The returned JSON includes complete recorded tool payloads and
// explicit accepted/durable sequences; it never derives state from the
// frontend's currently loaded transcript window.
func (c *Controller) ExportGoalDiagnostics(ctx context.Context, metadata GoalDiagnosticMetadata) ([]byte, error) {
	if c == nil {
		return nil, sessionv3.ErrSessionNotRunning
	}
	_, runtime, exclusive := c.v3Binding()
	if !exclusive || runtime == nil {
		return nil, errors.New("goal diagnostics require a linear v3 session")
	}
	temporaryRoot, err := os.MkdirTemp("", "reasonix-goal-diagnostics-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temporaryRoot)
	frozen := filepath.Join(temporaryRoot, "session")
	// Store.Export owns the commit and drain boundaries around its Flush, so the
	// diagnostic never pairs a manifest from one prefix with events from another.
	if err := runtime.Session().Export(ctx, frozen); err != nil {
		return nil, err
	}
	commits, err := sessionv3.Replay(frozen, nil)
	if err != nil {
		return nil, err
	}
	if metadata.Capabilities == nil {
		metadata.Capabilities = []string{}
	}
	fillGoalDiagnosticBuildMetadata(&metadata)
	through := uint64(0)
	if len(commits) > 0 {
		through = commits[len(commits)-1].LastSequence()
	}
	export := goalDiagnosticExport{
		SchemaVersion:     1,
		ExportedAt:        time.Now().UTC(),
		Metadata:          metadata,
		Runtime:           runtime.Snapshot(),
		Observation:       c.RuntimeStateSnapshot(),
		AcceptedThrough:   through,
		DurableThrough:    through,
		Commits:           commits,
		ActivationChanges: goalActivationChanges(commits),
		Unavailable:       []string{},
	}
	return json.MarshalIndent(export, "", "  ")
}

func fillGoalDiagnosticBuildMetadata(metadata *GoalDiagnosticMetadata) {
	if metadata == nil {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if metadata.ApplicationVersion == "" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		metadata.ApplicationVersion = info.Main.Version
	}
	if metadata.BuildCommit != "" {
		return
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			metadata.BuildCommit = setting.Value
			return
		}
	}
}

func goalActivationChanges(commits []sessionv3.Commit) []goalDiagnosticTransition {
	changes := make([]goalDiagnosticTransition, 0)
	activation := goaldomain.ActivationDisarmed
	for _, commit := range commits {
		hasTurnStart := false
		for _, item := range commit.Events {
			hasTurnStart = hasTurnStart || item.Kind == "turn/start"
		}
		for _, item := range commit.Events {
			if item.Kind != "goal/state" {
				continue
			}
			var document struct {
				Current *goaldomain.Snapshot `json:"current"`
			}
			if json.Unmarshal(item.Payload, &document) != nil {
				continue
			}
			transition := goalDiagnosticTransition{Sequence: item.Sequence, OperationID: commit.OperationID, Activation: goaldomain.ActivationDisarmed}
			if document.Current != nil {
				transition.GoalID = document.Current.ID
				transition.Revision = document.Current.Revision
				transition.Phase = document.Current.Phase
				transition.RoundsStarted = document.Current.RoundsStarted
				if document.Current.Phase == goaldomain.PhaseActive {
					op := strings.ToLower(commit.OperationID)
					if hasTurnStart || strings.Contains(op, ":create") || strings.Contains(op, ":resume") || strings.Contains(op, "goal-control:set") {
						activation = goaldomain.ActivationArmed
					}
				} else {
					activation = goaldomain.ActivationDisarmed
				}
				transition.Activation = activation
			} else {
				activation = goaldomain.ActivationDisarmed
			}
			changes = append(changes, transition)
		}
	}
	return changes
}
