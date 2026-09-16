package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"reasonix/desktop/internal/workspacestate"
	"reasonix/internal/session"
)

// PurgeCanonicalSession permanently removes only archived sessions. Legacy
// migration originals are retained as upgrade evidence, with mappings acting
// as tombstones so background discovery cannot import them again.
func (a *App) PurgeCanonicalSession(ref session.SessionRef) error {
	if err := validateLocalSessionRef(ref); err != nil {
		return err
	}
	release := a.lockRuntimeMutation("purge archived session")
	defer release()
	if err := a.purgeCanonicalSession(a.bootContext(), ref); err != nil {
		return err
	}
	a.emitProjectTreeChanged()
	a.emitSessionTargetChange("session_deleted", SessionTargetChangeEvent{
		TargetKey: (SessionTarget{SessionRef: ref}).key(),
	})
	return nil
}

func (a *App) purgeCanonicalSessionWithOperation(ref session.SessionRef, operationID string) (SessionTarget, error) {
	if err := validateLocalSessionRef(ref); err != nil {
		return SessionTarget{}, err
	}
	target, err := a.resolveCanonicalPurgeTarget(ref)
	if err != nil {
		return SessionTarget{}, err
	}
	if target.Lifecycle != workspacestate.Archived && target.Lifecycle != workspacestate.Deleted {
		return SessionTarget{}, newSessionOperationError("archived", "Archive this session before deleting it.")
	}
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		operationID = "delete-" + strings.TrimPrefix(newTabID(), "tab_")
	}
	a.cancelAISessionTitle(target.key())
	release := a.lockRuntimeMutation("purge archived session")
	defer release()
	if err := a.purgeCanonicalSession(a.bootContext(), ref); err != nil {
		return SessionTarget{}, err
	}
	if state, loadErr := a.workspaceRegistry().Load(a.bootContext()); loadErr == nil {
		target.Lifecycle = state.SessionStates[ref.SessionID].Lifecycle
		target.LifecycleGeneration = state.SessionStates[ref.SessionID].Generation
	}
	a.emitProjectTreeChanged()
	a.emitSessionTargetChange("session_deleted", SessionTargetChangeEvent{
		TargetKey: target.key(), OperationID: operationID,
		LifecycleGeneration: target.LifecycleGeneration, WorkspaceID: target.WorkspaceID,
	})
	return target, nil
}

func (a *App) resolveCanonicalPurgeTarget(ref session.SessionRef) (SessionTarget, error) {
	target, resolveErr := a.resolveCanonicalSessionTargetState(ref, "", true)
	if resolveErr == nil {
		return target, nil
	}
	state, err := a.workspaceRegistry().Load(a.bootContext())
	if err != nil {
		return SessionTarget{}, err
	}
	op, pending := state.PendingOperations["purge-"+ref.SessionID]
	status, known := state.SessionStates[ref.SessionID]
	if !pending || op.Kind != "purge" || !known || status.Lifecycle != workspacestate.Deleted {
		return SessionTarget{}, resolveErr
	}
	target = a.runtimeSessionTarget("", ref, sessionRoute(ref.SessionID))
	target.SessionRef = ref
	target.SessionPath = sessionRoute(ref.SessionID)
	target.TopicID = state.Presentation[ref.SessionID].TopicID
	target.Lifecycle = status.Lifecycle
	target.LifecycleGeneration = status.Generation
	for id, workspace := range state.Workspaces {
		if containsDesktopString(workspace.SessionIDs, ref.SessionID) {
			target.WorkspaceID = id
			target.WorkspaceRoot = workspace.Root
			target.Scope = "project"
			if id == workspacestate.GlobalWorkspaceID {
				target.Scope, target.WorkspaceRoot = "global", ""
			}
			break
		}
	}
	return target, nil
}

func (a *App) purgeCanonicalSession(ctx context.Context, ref session.SessionRef, expected ...uint64) error {
	store := a.workspaceRegistry()
	state, err := store.Load(ctx)
	if err != nil {
		return err
	}
	op, retry := state.PendingOperations["purge-"+ref.SessionID]
	if retry && op.Phase == "committed" {
		return nil
	}
	if !retry && state.SessionStates[ref.SessionID].Lifecycle != workspacestate.Archived {
		return errors.New("only archived sessions can be permanently deleted")
	}
	if err := a.retireArchivedSessionRuntime(ctx, ref); err != nil {
		return fmt.Errorf("session runtime is still in use: %w", err)
	}
	filesystem := session.NewFilesystemPersistence(a.desktopSessions.root)
	if err := filesystem.PurgeWithTombstone(ctx, ref.SessionID, func() error {
		if err := store.BeginPurge(ctx, ref.SessionID, expected...); err != nil {
			return err
		}
		return store.AdvancePurge(ctx, ref.SessionID, "tombstoned")
	}); err != nil {
		return err
	}
	if err := store.AdvancePurge(ctx, ref.SessionID, "content_removed"); err != nil {
		return err
	}
	return store.CompletePurge(ctx, ref.SessionID)
}

// Client release may retain an idle runtime for fast navigation. Archive and
// purge must retire that cache entry; Service.Close still refuses bound clients
// and executing turns, so this cannot close a live user's session underneath it.
func (a *App) retireArchivedSessionRuntime(ctx context.Context, ref session.SessionRef) error {
	service := a.desktopSessionService("")
	if _, live := service.Runtime(ref); !live {
		return nil
	}
	err := service.Close(ctx, ref)
	if errors.Is(err, session.ErrSessionNotRunning) {
		return nil
	}
	return err
}
