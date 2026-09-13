package control

import (
	"context"
	"encoding/json"
	"errors"
	"runtime/debug"
	"strings"
	"time"

	goaldomain "reasonix/internal/goal"
	"reasonix/internal/secrets"
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
	SchemaVersion     int                         `json:"schemaVersion"`
	ExportedAt        time.Time                   `json:"exportedAt"`
	Metadata          GoalDiagnosticMetadata      `json:"metadata"`
	Runtime           sessionv3.RuntimeSnapshot   `json:"runtime"`
	Observation       any                         `json:"observation"`
	AcceptedThrough   uint64                      `json:"acceptedThrough"`
	DurableThrough    uint64                      `json:"durableThrough"`
	PersistenceStatus sessionv3.PersistenceStatus `json:"persistenceStatus"`
	PersistenceError  string                      `json:"persistenceError,omitempty"`
	Commits           []sessionv3.Commit          `json:"commits"`
	ActivationChanges []goalDiagnosticTransition  `json:"activationChanges"`
	Unavailable       []string                    `json:"unavailable"`
}

type goalDiagnosticTransition struct {
	Sequence      uint64                `json:"sequence"`
	OperationID   string                `json:"operationId"`
	GoalID        string                `json:"goalId,omitempty"`
	Revision      uint64                `json:"revision,omitempty"`
	Phase         goaldomain.Phase      `json:"phase,omitempty"`
	Activation    goaldomain.Activation `json:"activation"`
	RoundsStarted uint64                `json:"roundsStarted,omitempty"`
	Inferred      bool                  `json:"inferred"`
}

// ExportGoalDiagnostics reads the authoritative accepted v3 event prefix and
// attempts a durability checkpoint first. A failed Flush is evidence to export,
// not a reason to hide the events and persistence status needed to diagnose it.
// Credential-like material is redacted at this explicit diagnostic boundary.
func (c *Controller) ExportGoalDiagnostics(ctx context.Context, metadata GoalDiagnosticMetadata) ([]byte, error) {
	if c == nil {
		return nil, sessionv3.ErrSessionNotRunning
	}
	_, runtime, exclusive := c.v3Binding()
	if !exclusive || runtime == nil {
		return nil, errors.New("goal diagnostics require a linear v3 session")
	}
	flushErr := error(nil)
	if _, err := runtime.Session().Flush(ctx); err != nil {
		flushErr = err
	}
	runtimeSnapshot := runtime.StateSnapshot()
	runtimeSnapshot.Session.PersistenceError = secrets.RedactCredentials(runtimeSnapshot.Session.PersistenceError)
	commits, err := acceptedGoalDiagnosticCommits(ctx, runtime.Session(), runtimeSnapshot.Session.EventSequence)
	if err != nil {
		return nil, err
	}
	if metadata.Capabilities == nil {
		metadata.Capabilities = []string{}
	}
	fillGoalDiagnosticBuildMetadata(&metadata)
	unavailable := []string{
		"activation transitions are inferred from recorded Goal events; process-local activation history before export is unavailable",
	}
	if flushErr != nil {
		unavailable = append(unavailable, "durability checkpoint failed: "+secrets.RedactError(flushErr))
	}
	observation := c.RuntimeStateSnapshot()
	observation.PersistenceErr = secrets.RedactCredentials(observation.PersistenceErr)
	export := goalDiagnosticExport{
		SchemaVersion:     1,
		ExportedAt:        time.Now().UTC(),
		Metadata:          metadata,
		Runtime:           runtimeSnapshot,
		Observation:       observation,
		AcceptedThrough:   runtimeSnapshot.Session.EventSequence,
		DurableThrough:    runtimeSnapshot.Session.DurableSequence,
		PersistenceStatus: runtimeSnapshot.Session.PersistenceStatus,
		PersistenceError:  runtimeSnapshot.Session.PersistenceError,
		Commits:           commits,
		ActivationChanges: goalActivationChanges(commits),
		Unavailable:       unavailable,
	}
	payload, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return nil, err
	}
	redacted := []byte(secrets.Redact(string(payload)))
	if !json.Valid(redacted) {
		return nil, errors.New("redacted goal diagnostics are not valid JSON")
	}
	return redacted, nil
}

func acceptedGoalDiagnosticCommits(ctx context.Context, session *sessionv3.Session, through uint64) ([]sessionv3.Commit, error) {
	commits := make([]sessionv3.Commit, 0)
	offset := uint64(0)
	for {
		page, err := session.AcceptedPage(ctx, offset, 1000)
		if err != nil {
			return nil, err
		}
		for _, commit := range page.Commits {
			if commit.LastSequence() > through {
				return commits, nil
			}
			commits = append(commits, commit)
		}
		if !page.Truncated {
			return commits, nil
		}
		if page.Next <= offset {
			return nil, errors.New("goal diagnostics accepted-page cursor did not advance")
		}
		offset = page.Next
	}
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
			transition := goalDiagnosticTransition{Sequence: item.Sequence, OperationID: commit.OperationID, Activation: goaldomain.ActivationDisarmed, Inferred: true}
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
