package main

import (
	"fmt"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/provider"
)

// carriedTabHistory is what a rebuild carries into the replacement runtime:
// a live controller's in-memory history, or the transcript reloaded from disk
// when a failed startup left the tab without one.
type carriedTabHistory struct {
	messages []provider.Message
	session  *agent.Session
	prevPath string
	// acquiredLease is set when the carry took the session lease for a tab
	// that held none, so a failed switch can return it (see releaseOnFailure).
	acquiredLease bool
}

// carryTabHistoryForModelSwitch takes the session lease before reading either
// source, so the replacement never resumes a transcript another writer owns.
func (a *App) carryTabHistoryForModelSwitch(tab *WorkspaceTab, oldCtrl control.SessionAPI, prevPath string) (carriedTabHistory, error) {
	carried := carriedTabHistory{prevPath: prevPath}
	if oldCtrl != nil && carried.prevPath == "" {
		carried.prevPath = oldCtrl.SessionPath()
	}
	if oldCtrl == nil && carried.prevPath == "" {
		return carried, nil
	}
	if err := a.ensureTabSessionLeaseForRebuild(tab, carried.prevPath, "model"); err != nil {
		return carried, err
	}
	if oldCtrl == nil {
		carried.acquiredLease = true
		session, err := agent.LoadSession(carried.prevPath)
		if err != nil {
			carried.releaseOnFailure(tab)
			return carried, err
		}
		carried.session = session
		return carried, nil
	}
	if err := a.snapshotTabForAction(tab, "changing model"); err != nil {
		return carried, err
	}
	carried.prevPath = sessionPathAfterSnapshot(oldCtrl, carried.prevPath)
	carried.messages = oldCtrl.History()
	return carried, nil
}

// releaseOnFailure returns a lease the carry acquired for a controller-less
// tab, restoring the failed-startup state where another process may open it.
func (c carriedTabHistory) releaseOnFailure(tab *WorkspaceTab) {
	if c.acquiredLease && c.prevPath != "" {
		tab.releaseSessionLeaseForKey(sessionRuntimeKey(c.prevPath))
	}
}

func (c carriedTabHistory) resume(ctrl *control.Controller, path string, runtime normalizedTabRuntime) (normalizedTabRuntime, error) {
	if c.session != nil {
		return resumeControllerRuntimeWithSession(ctrl, c.session, path, runtime)
	}
	return resumeControllerRuntimeWithMessages(ctrl, c.messages, path, runtime)
}

// tabSelectionUnchanged reports whether a model request already matches the
// running selection. The same name over an edited connection is a real change,
// so the identity, not the ref alone, decides.
func (a *App) tabSelectionUnchanged(ctrl control.SessionAPI, workspaceRoot, currentModel, name string) (bool, error) {
	if name != currentModel || ctrl == nil {
		return false, nil
	}
	cfg, err := config.LoadForRoot(workspaceRoot)
	if err != nil {
		return false, err
	}
	return cfg.ModelSelectionIdentity(name) == controllerModelSelectionIdentity(ctrl), nil
}

// savedModelForSettingsRebuild recovers the model an implicit settings rebuild
// must restore, validating it against the connection it was recorded against.
// An explicit override skips the check because it is a new selection.
func (a *App) savedModelForSettingsRebuild(cfg *config.Config, selection tabRuntimeSnapshot, oldCtrl control.SessionAPI, modelOverride, prevPath string) (string, error) {
	if strings.TrimSpace(modelOverride) != "" {
		return "", nil
	}
	if oldCtrl != nil {
		return cfg.ResolveSavedModel(selection.model, controllerModelSelectionIdentity(oldCtrl))
	}
	model, identity, ok := agent.LoadSessionModelSelection(prevPath)
	if !ok {
		return "", nil
	}
	resolved, err := cfg.ResolveSavedModel(model, identity)
	if err != nil {
		return "", err
	}
	// An unaliased sidecar ref outside the catalog keeps the tab's own model
	// and its existing stale-selection policy (same gate as startup).
	if _, ok := cfg.ResolveModel(resolved); !ok {
		return "", nil
	}
	return resolved, nil
}

func (a *App) settingsRebuildModel(tab *WorkspaceTab, cfg *config.Config, setting, model string) (string, error) {
	if setting == "saved model settings" {
		return resolveModelSettingsRuntime(cfg, model)
	}
	resolved, fallback, ok := cfg.ResolveModelWithFallback(model)
	if !ok {
		return model, nil
	}
	if fallback && strings.TrimSpace(model) != "" {
		a.noticeForTab(tab.ID, fmt.Sprintf("model %q is no longer available; switched to %s", model, resolved))
	}
	return resolved, nil
}
