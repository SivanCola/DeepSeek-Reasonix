package main

import (
	"errors"
	"fmt"
	"strings"

	"reasonix/internal/control"
	"reasonix/internal/session"
)

type SessionOperationMode int

const (
	OperationPersistent SessionOperationMode = iota
	OperationRuntime
)

const (
	sessionOperationTargetNotFound  = "target_not_found"
	sessionOperationNoMessages      = "no_messages"
	sessionOperationRuntimeNotOpen  = "runtime_not_open"
	sessionOperationRuntimeNotReady = "runtime_not_ready"
	sessionOperationTitleConflict   = "title_conflict"
	sessionOperationBusy            = "operation_busy"
)

// SessionOperationError is stable at the host boundary: the code is intended
// for frontend localization while the message remains safe for older clients.
type SessionOperationError struct {
	Code    string
	Message string
}

func (e *SessionOperationError) Error() string {
	if e == nil {
		return ""
	}
	return "session_operation:" + e.Code + ":" + e.Message
}

func newSessionOperationError(code, message string) error {
	return &SessionOperationError{Code: code, Message: message}
}

// SessionTarget resolves durable identity independently from runtime state.
// Controller is optional and is never used to decide whether the session
// exists.
type SessionTarget struct {
	TopicID       string
	SessionRef    session.SessionRef
	SessionPath   string
	Scope         string
	WorkspaceRoot string
	IsOpen        bool
	Ready         bool
	TabID         string
	Controller    *control.Controller
}

type sessionTargetSelector struct {
	Ref         *session.SessionRef
	SessionPath string
	TopicID     string
}

func (target SessionTarget) key() string {
	if strings.TrimSpace(target.SessionRef.SessionID) != "" {
		return "ref:" + target.SessionRef.HostID + ":" + target.SessionRef.SessionID
	}
	if path := strings.TrimSpace(target.SessionPath); path != "" {
		return "path:" + sessionRuntimeKey(path)
	}
	return "topic:" + strings.TrimSpace(target.TopicID)
}

func (target SessionTarget) RequireRuntime() (*control.Controller, error) {
	if !target.IsOpen || target.Controller == nil {
		return nil, newSessionOperationError(sessionOperationRuntimeNotOpen, "Open this session before using this action.")
	}
	if !target.Ready {
		return nil, newSessionOperationError(sessionOperationRuntimeNotReady, "This session is still loading. Try again shortly.")
	}
	return target.Controller, nil
}

func (a *App) resolveSessionTarget(selector sessionTargetSelector) (SessionTarget, error) {
	if selector.Ref != nil && strings.TrimSpace(selector.Ref.SessionID) != "" {
		return a.resolveCanonicalSessionTarget(*selector.Ref, strings.TrimSpace(selector.TopicID))
	}
	if path := strings.TrimSpace(selector.SessionPath); path != "" {
		if ref, ok := sessionRefForRoute(a.desktopSessionService(""), path); ok {
			return a.resolveCanonicalSessionTarget(ref, strings.TrimSpace(selector.TopicID))
		}
		return a.resolveLegacySessionTarget(path, strings.TrimSpace(selector.TopicID))
	}
	topicID := strings.TrimSpace(selector.TopicID)
	if topicID == "" {
		return SessionTarget{}, newSessionOperationError(sessionOperationTargetNotFound, "The session no longer exists.")
	}
	if ref, ok := a.canonicalSessionRefForTopic(topicID); ok {
		return a.resolveCanonicalSessionTarget(ref, topicID)
	}
	if runtime := a.runtimeSessionTarget(topicID, session.SessionRef{}, ""); runtime.Controller != nil {
		if ref, ok := runtime.Controller.SessionRef(); ok {
			target, err := a.resolveCanonicalSessionTarget(ref, topicID)
			if err == nil {
				return target, nil
			}
		}
	}
	scope, root, ok := a.findTopicLocation(topicID)
	if !ok {
		return SessionTarget{}, newSessionOperationError(sessionOperationTargetNotFound, "The session no longer exists.")
	}
	path := a.catalogSessionPathForTopic(scope, root, topicID)
	if strings.TrimSpace(path) == "" {
		if runtime := a.runtimeSessionTarget(topicID, session.SessionRef{}, ""); runtime.IsOpen {
			runtime.Scope, runtime.WorkspaceRoot = scope, root
			return runtime, nil
		}
		for _, dir := range a.knownSessionDirs() {
			matches := topicSessionMatches(dir, topicID)
			if len(matches) == 0 {
				continue
			}
			path = matches[0].path
			break
		}
		if strings.TrimSpace(path) == "" {
			// A newly created topic can exist before its first user turn has
			// allocated physical session storage. It is a valid empty target,
			// not a missing one.
			return SessionTarget{TopicID: topicID, Scope: scope, WorkspaceRoot: root}, nil
		}
	}
	target, err := a.resolveLegacySessionTarget(path, topicID)
	if err == nil {
		target.Scope, target.WorkspaceRoot = scope, root
	}
	return target, err
}

func (a *App) canonicalSessionRefForTopic(topicID string) (session.SessionRef, bool) {
	state, err := a.workspaceRegistry().Load(a.bootContext())
	if err != nil {
		return session.SessionRef{}, false
	}
	for _, workspace := range state.Workspaces {
		for _, id := range workspace.SessionIDs {
			presentation := state.Presentation[id]
			if presentation.TopicID == topicID || "canonical-"+id == topicID || id == topicID || sessionRoute(id) == topicID {
				return session.SessionRef{HostID: localDesktopHostID, SessionID: id}, true
			}
		}
	}
	return session.SessionRef{}, false
}

func (a *App) resolveCanonicalSessionTarget(ref session.SessionRef, topicID string) (SessionTarget, error) {
	if err := validateLocalSessionRef(ref); err != nil {
		return SessionTarget{}, newSessionOperationError(sessionOperationTargetNotFound, "The session no longer exists.")
	}
	if _, err := a.desktopSessionService("").Query().Stat(a.bootContext(), ref); err != nil {
		return SessionTarget{}, newSessionOperationError(sessionOperationTargetNotFound, "The session no longer exists.")
	}
	target := a.runtimeSessionTarget(topicID, ref, sessionRoute(ref.SessionID))
	target.SessionRef = ref
	target.SessionPath = sessionRoute(ref.SessionID)
	if target.TopicID == "" {
		target.TopicID = topicID
	}
	state, loadErr := a.workspaceRegistry().Load(a.bootContext())
	if loadErr == nil {
		for _, workspace := range state.Workspaces {
			for _, id := range workspace.SessionIDs {
				if id != ref.SessionID {
					continue
				}
				target.WorkspaceRoot = workspace.Root
				target.Scope = "project"
				if workspace.ID == "global" {
					target.Scope, target.WorkspaceRoot = "global", ""
				}
				if target.TopicID == "" {
					target.TopicID = state.Presentation[id].TopicID
				}
			}
		}
	}
	if target.TopicID == "" {
		target.TopicID = "canonical-" + ref.SessionID
	}
	return target, nil
}

func (a *App) resolveLegacySessionTarget(path, topicID string) (SessionTarget, error) {
	dir, validated, err := a.sessionDirForPath(path)
	if err != nil {
		return SessionTarget{}, newSessionOperationError(sessionOperationTargetNotFound, "The session no longer exists.")
	}
	if _, _, err := validateSessionPath(dir, validated); err != nil {
		return SessionTarget{}, newSessionOperationError(sessionOperationTargetNotFound, "The session no longer exists.")
	}
	target := a.runtimeSessionTarget(topicID, session.SessionRef{}, validated)
	target.SessionPath = validated
	target.TopicID = topicID
	return target, nil
}

func (a *App) runtimeSessionTarget(topicID string, ref session.SessionRef, path string) SessionTarget {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil {
			continue
		}
		ctrl, _ := tab.Ctrl.(*control.Controller)
		matches := topicID != "" && (tab.TopicID == topicID || tab.SessionID == topicID || sessionRoute(tab.SessionID) == topicID)
		if !matches && ref.SessionID != "" && ctrl != nil {
			if bound, ok := ctrl.SessionRef(); ok {
				matches = bound == ref
			}
		}
		if !matches && path != "" {
			matches = sessionRuntimeKey(tab.currentSessionPath()) == sessionRuntimeKey(path)
		}
		if !matches {
			continue
		}
		resolvedPath := path
		if resolvedPath == "" {
			resolvedPath = tab.currentSessionPath()
		}
		resolvedRef := ref
		if resolvedRef.SessionID == "" && ctrl != nil {
			if bound, ok := ctrl.SessionRef(); ok {
				resolvedRef = bound
			}
		}
		return SessionTarget{
			TopicID: topicID, SessionRef: resolvedRef, SessionPath: resolvedPath,
			Scope: tab.Scope, WorkspaceRoot: tab.WorkspaceRoot,
			IsOpen: true, Ready: tab.Ready, TabID: tab.ID, Controller: ctrl,
		}
	}
	return SessionTarget{TopicID: topicID, SessionRef: ref, SessionPath: path}
}

func (a *App) sessionTargetStillOwnsRuntime(target SessionTarget) bool {
	if !target.IsOpen || target.Controller == nil || target.TabID == "" {
		return !target.IsOpen
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	tab := a.tabs[target.TabID]
	if tab == nil || tab.Ctrl != target.Controller {
		return false
	}
	if target.SessionRef.SessionID != "" {
		ref, ok := target.Controller.SessionRef()
		return ok && ref == target.SessionRef
	}
	return sessionRuntimeKey(tab.currentSessionPath()) == sessionRuntimeKey(target.SessionPath)
}

func sessionOperationConflict(err error) error {
	if errors.Is(err, session.ErrSessionTitleChanged) {
		return newSessionOperationError(sessionOperationTitleConflict, "The session title changed while AI rename was running. Try again.")
	}
	return fmt.Errorf("AI rename session: %w", err)
}
