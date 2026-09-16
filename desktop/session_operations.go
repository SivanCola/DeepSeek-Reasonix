package main

import (
	"errors"
	"strconv"
	"strings"

	"reasonix/internal/agent"
)

// SessionMutationResult is the stable response for an explicitly targeted
// persistent session operation. Versions are strings at the RPC boundary so
// JavaScript never truncates durable 64-bit sequence identities.
type SessionMutationResult struct {
	TargetKey           string `json:"targetKey"`
	OperationID         string `json:"operationId"`
	Committed           bool   `json:"committed"`
	Title               string `json:"title,omitempty"`
	TitleVersion        string `json:"titleVersion,omitempty"`
	LifecycleGeneration uint64 `json:"lifecycleGeneration"`
	ProjectionPending   bool   `json:"projectionPending,omitempty"`
}

// SessionTargetChangeEvent is an incremental projection hint. Durable storage
// remains authoritative; consumers that miss or cannot order these events
// re-read only the named target.
type SessionTargetChangeEvent struct {
	TargetKey           string `json:"targetKey"`
	OperationID         string `json:"operationId,omitempty"`
	LifecycleGeneration uint64 `json:"lifecycleGeneration"`
	Title               string `json:"title,omitempty"`
	WorkspaceID         string `json:"workspaceId,omitempty"`
}

func (a *App) emitSessionTargetChange(name string, event SessionTargetChangeEvent) {
	a.emitRuntimeEvent(name, event)
}

func sessionOperationErrorForTarget(err error, targetKey, operationID string) error {
	if err == nil {
		return nil
	}
	var operationErr *SessionOperationError
	if errors.As(err, &operationErr) {
		copy := *operationErr
		if copy.TargetKey == "" {
			copy.TargetKey = targetKey
		}
		if copy.OperationID == "" {
			copy.OperationID = operationID
		}
		return &copy
	}
	// RPC messages are user-visible. Do not pass through paths, lease holder
	// details, provider bodies, or credential-adjacent diagnostics from an
	// unclassified lower-level error.
	return &SessionOperationError{
		Code: sessionOperationFailed, Message: "Unable to complete this session operation.",
		TargetKey: targetKey, OperationID: operationID,
	}
}

// RenameSessionTarget performs a manual persistent rename without opening or
// selecting the target session.
func (a *App) RenameSessionTarget(selector SessionSelector, title string) (SessionMutationResult, error) {
	target, err := a.resolveSessionTarget(selector)
	if err != nil {
		return SessionMutationResult{}, err
	}
	key := target.key()
	operationID := "title-manual-" + strings.TrimPrefix(newTabID(), "tab_")
	a.cancelAISessionTitle(key)
	title = strings.TrimSpace(title)
	if target.SessionRef.SessionID != "" {
		err = a.RenameCanonicalSession(target.SessionRef, title)
		if err == nil {
			info, statErr := a.desktopSessionService("").Query().Stat(a.bootContext(), target.SessionRef)
			if statErr != nil {
				result := SessionMutationResult{
					TargetKey: key, OperationID: operationID, Committed: true, Title: title,
					LifecycleGeneration: target.LifecycleGeneration, ProjectionPending: true,
				}
				a.emitSessionTargetChange("session_metadata_changed", SessionTargetChangeEvent{
					TargetKey: key, OperationID: operationID, LifecycleGeneration: target.LifecycleGeneration,
					Title: title, WorkspaceID: target.WorkspaceID,
				})
				return result, nil
			}
			result := SessionMutationResult{
				TargetKey: key, OperationID: operationID, Committed: true, Title: title,
				TitleVersion:        titleSequenceVersion(info.TitleSequence),
				LifecycleGeneration: target.LifecycleGeneration,
			}
			a.emitSessionTargetChange("session_metadata_changed", SessionTargetChangeEvent{
				TargetKey: key, OperationID: operationID, LifecycleGeneration: target.LifecycleGeneration, Title: title,
				WorkspaceID: target.WorkspaceID,
			})
			return result, nil
		}
	} else if target.SessionPath != "" {
		err = a.RenameSession(target.SessionPath, title)
		if err == nil {
			_, revision, revisionErr := agent.SessionTitleSnapshot(target.SessionPath)
			result := SessionMutationResult{
				TargetKey: key, OperationID: operationID, Committed: true, Title: title,
				TitleVersion: revision, LifecycleGeneration: target.LifecycleGeneration,
				ProjectionPending: revisionErr != nil,
			}
			a.emitSessionTargetChange("session_metadata_changed", SessionTargetChangeEvent{
				TargetKey: key, OperationID: operationID, LifecycleGeneration: target.LifecycleGeneration, Title: title,
			})
			return result, nil
		}
	} else if target.TopicID != "" {
		err = a.RenameTopic(target.TopicID, title)
		if err == nil {
			result := SessionMutationResult{
				TargetKey: key, OperationID: operationID, Committed: true, Title: title,
				LifecycleGeneration: target.LifecycleGeneration,
			}
			a.emitSessionTargetChange("session_metadata_changed", SessionTargetChangeEvent{
				TargetKey: key, OperationID: operationID, LifecycleGeneration: target.LifecycleGeneration, Title: title,
			})
			return result, nil
		}
	} else {
		err = newSessionOperationError(sessionOperationTargetNotFound, "The session no longer exists.")
	}
	return SessionMutationResult{}, sessionOperationErrorForTarget(err, key, operationID)
}

func titleSequenceVersion(sequence uint64) string {
	if sequence == 0 {
		return ""
	}
	return "event:" + strconv.FormatUint(sequence, 10)
}
