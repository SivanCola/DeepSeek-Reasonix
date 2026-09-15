package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"

	"reasonix/desktop/internal/workspacestate"
	"reasonix/internal/config"
	"reasonix/internal/session"
)

type freshSessionCreator interface {
	BindFreshSession(context.Context, string) (session.SessionRef, error)
}

func desktopWorkspaceID(scope, workspaceRoot string) string {
	if strings.TrimSpace(scope) != "project" {
		return workspacestate.GlobalWorkspaceID
	}
	root := canonicalRuntimeRoot(workspaceRoot)
	digest := sha256.Sum256([]byte(root))
	return "project-" + hex.EncodeToString(digest[:12])
}

func desktopWorkspaceRoot(scope, workspaceRoot string) string {
	if strings.TrimSpace(scope) != "project" {
		return globalWorkspaceRoot()
	}
	return filepath.Clean(strings.TrimSpace(workspaceRoot))
}

func (a *App) workspaceRegistry() *workspacestate.Store {
	if a == nil {
		return nil
	}
	a.sessionServicesMu.Lock()
	defer a.sessionServicesMu.Unlock()
	if a.workspaceState == nil {
		a.workspaceState = workspacestate.NewStore(config.DesktopWorkspaceStatePath())
	}
	return a.workspaceState
}

func (a *App) ensureDesktopWorkspace(ctx context.Context, scope, workspaceRoot string) (string, error) {
	store := a.workspaceRegistry()
	if store == nil || store.Path() == "" || store.Path() == "." {
		return "", errors.New("desktop workspace registry is unavailable")
	}
	id := desktopWorkspaceID(scope, workspaceRoot)
	title := globalProjectTitle()
	if strings.TrimSpace(scope) == "project" {
		title = workspaceName(workspaceRoot)
	}
	err := store.EnsureWorkspace(ctx, workspacestate.Workspace{
		ID: id, Root: desktopWorkspaceRoot(scope, workspaceRoot), Title: title, Visible: true,
	})
	return id, err
}

func (a *App) bindFreshDesktopSession(ctx context.Context, scope, workspaceRoot string, creator freshSessionCreator) (session.SessionRef, string, error) {
	workspaceID, err := a.ensureDesktopWorkspace(ctx, scope, workspaceRoot)
	if err != nil {
		return session.SessionRef{}, "", err
	}
	sessionID := "desktop-" + strings.TrimPrefix(newTabID(), "tab_")
	operationID := "create-" + strings.TrimPrefix(newTabID(), "tab_")
	store := a.workspaceRegistry()
	if err := store.BeginCreate(ctx, workspacestate.PendingCreate{OperationID: operationID, WorkspaceID: workspaceID, SessionID: sessionID}); err != nil {
		return session.SessionRef{}, "", err
	}
	options := session.CreateOptions{SessionID: sessionID, CWD: desktopWorkspaceRoot(scope, workspaceRoot), Origin: session.SessionOriginNew}
	var ref session.SessionRef
	if headerCreator, ok := creator.(interface {
		BindFreshSessionWithOptions(context.Context, session.CreateOptions) (session.SessionRef, error)
	}); ok {
		ref, err = headerCreator.BindFreshSessionWithOptions(ctx, options)
	} else {
		ref, err = creator.BindFreshSession(ctx, sessionID)
	}
	if err != nil {
		return session.SessionRef{}, workspaceID, err
	}
	if err := store.AttachSession(ctx, operationID, workspaceID, ref.SessionID, ""); err != nil {
		return ref, workspaceID, err
	}
	return ref, workspaceID, nil
}

func (a *App) attachDesktopSession(ctx context.Context, scope, workspaceRoot string, ref session.SessionRef) (string, error) {
	workspaceID, err := a.ensureDesktopWorkspace(ctx, scope, workspaceRoot)
	if err != nil {
		return "", err
	}
	if err := a.workspaceRegistry().AttachSession(ctx, "", workspaceID, ref.SessionID, ""); err != nil {
		return "", err
	}
	return workspaceID, nil
}
