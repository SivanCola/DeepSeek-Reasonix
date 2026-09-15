package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/session"
)

var errSessionNavigationSuperseded = errors.New("session navigation was superseded")

func (a *App) continueLegacySessionForTranscript(tab *WorkspaceTab, ctrl control.SessionAPI, sourcePath string, limit int, includeHistory, readOnly bool) (HistoryPage, error) {
	identity, ok := ctrl.(control.IdentityLifecycle)
	if !ok || !identity.UsesExclusiveSession() {
		return HistoryPage{}, fmt.Errorf("session identity protocol is unavailable")
	}

	a.runtimeRebuildMu.Lock()
	defer a.runtimeRebuildMu.Unlock()
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()

	current := a.controllerForTab(tab)
	if current != ctrl || current == nil {
		return HistoryPage{}, fmt.Errorf("tab runtime changed while continuing legacy session")
	}
	if current.RuntimeStatus().Running || current.RuntimeStatus().PendingPrompt {
		return HistoryPage{}, control.ErrTurnRunning
	}
	if err := current.Snapshot(); err != nil {
		return HistoryPage{}, err
	}
	a.mu.RLock()
	createOptions := desktopLegacyImportOptions(snapshotTabRuntimeLocked(tab).workspaceRoot)
	a.mu.RUnlock()
	var err error
	if creator, ok := identity.(control.IdentityCreateLifecycle); ok {
		_, err = creator.ContinueLegacySessionWithOptions(a.bootContext(), sourcePath, "", createOptions)
	} else {
		_, err = identity.ContinueLegacySession(a.bootContext(), sourcePath, "")
	}
	if err != nil {
		return HistoryPage{}, err
	}
	a.syncTabSessionIdentity(tab, current)
	a.setTabReadOnly(tab.ID, readOnly)
	a.invalidatePromptHistoryCache()
	a.notifyTabRuntimeRebuilt(tab)
	if !includeHistory {
		return HistoryPage{Messages: []HistoryMessage{}}, nil
	}
	return historyPageFromMessagesForTab(tab, current, current.History(), 0, limit), nil
}

func (a *App) resumeCanonicalSessionForTranscript(tab *WorkspaceTab, ctrl control.SessionAPI, route string, limit int, includeHistory bool, navigationSequence ...uint64) (HistoryPage, error) {
	identity, ok := ctrl.(control.IdentityLifecycle)
	if !ok || !identity.UsesExclusiveSession() {
		return HistoryPage{}, fmt.Errorf("session identity protocol is unavailable")
	}
	service := identity.SessionService()
	ref, ok := sessionRefForRoute(service, route)
	if !ok {
		return HistoryPage{}, fmt.Errorf("invalid session identity")
	}

	a.runtimeRebuildMu.Lock()
	defer a.runtimeRebuildMu.Unlock()
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	wantedNavigation := uint64(0)
	if len(navigationSequence) > 0 {
		wantedNavigation = navigationSequence[0]
		if a.desktopSessions.navigationSeq.Load() != wantedNavigation {
			return HistoryPage{}, errSessionNavigationSuperseded
		}
	}

	current := a.controllerForTab(tab)
	if current != ctrl || current == nil {
		return HistoryPage{}, fmt.Errorf("tab runtime changed while opening session")
	}
	if current.RuntimeStatus().Running || current.RuntimeStatus().PendingPrompt {
		return HistoryPage{}, control.ErrTurnRunning
	}
	if currentRef, bound := identity.SessionRef(); !bound || currentRef != ref {
		if err := current.Snapshot(); err != nil {
			return HistoryPage{}, err
		}
		binding, err := service.EnsureExecution(a.bootContext(), ref)
		if err != nil {
			return HistoryPage{}, err
		}
		defer func() { _ = binding.Release(a.bootContext()) }()
		targetModel := strings.TrimSpace(binding.Runtime().StateSnapshot().Session.Projection.ModelRef)
		if targetModel != "" {
			current, err = a.replaceControllerForSessionOpenLocked(tab, current, service, ref, targetModel, wantedNavigation)
			if err != nil {
				return HistoryPage{}, err
			}
		} else {
			if wantedNavigation != 0 && a.desktopSessions.navigationSeq.Load() != wantedNavigation {
				return HistoryPage{}, errSessionNavigationSuperseded
			}
			if _, err := identity.OpenSession(a.bootContext(), ref); err != nil {
				return HistoryPage{}, err
			}
		}
	}
	a.syncTabSessionIdentity(tab, current)
	a.setTabReadOnly(tab.ID, false)
	a.invalidatePromptHistoryCache()
	a.notifyTabRuntimeRebuilt(tab)
	if !includeHistory {
		return HistoryPage{Messages: []HistoryMessage{}}, nil
	}
	return historyPageFromMessagesForTab(tab, current, current.History(), 0, limit), nil
}

// replaceControllerForSessionOpenLocked prepares an Agent for the target session's
// recorded model before publishing it to the tab. The caller holds
// runtimeRebuildMu and tab.turnStartMu, so the source remains usable until the
// target model, writer, and event projection have all been validated.
func (a *App) replaceControllerForSessionOpenLocked(tab *WorkspaceTab, current control.SessionAPI, service *session.Service, ref session.SessionRef, targetModel string, navigationSequence ...uint64) (control.SessionAPI, error) {
	if tab == nil || current == nil || service == nil {
		return nil, fmt.Errorf("session runtime changed while opening session")
	}
	transition, err := a.reserveSessionRuntimePath(tab, sessionRoute(ref.SessionID))
	if err != nil {
		return nil, userFacingSessionLeaseError("", err)
	}
	committed := false
	defer func() {
		if !committed {
			a.rollbackSessionRuntimePath(transition)
		}
	}()
	snap := a.tabRuntimeSnapshot(tab)
	root := strings.TrimSpace(snap.workspaceRoot)
	if root == "" {
		if wd, err := os.Getwd(); err == nil {
			root = wd
		}
	}
	cfg, err := config.LoadForRoot(root)
	if err != nil {
		return nil, err
	}
	sharedHost := a.lookupSharedHost(snap.sharedHostKey)
	extensionGeneration := a.currentExtensionGeneration()
	buildOptions := a.sessionOpenBootOptions(tab, snap, cfg, service, sharedHost, root, targetModel)
	requestedModel := targetModel
	candidate, targetModel, fallbackUsed, err := a.buildSessionOpenControllerCandidate(
		a.bootContext(), extensionGeneration, cfg, buildOptions,
	)
	if err != nil {
		return nil, err
	}
	discard := true
	defer func() {
		if discard {
			candidate.Close()
		}
	}()
	candidateIdentity, ok := candidate.(control.IdentityLifecycle)
	if !ok || !candidateIdentity.UsesExclusiveSession() {
		return nil, fmt.Errorf("replacement session identity protocol is unavailable")
	}
	if _, err := candidateIdentity.OpenSession(a.bootContext(), ref); err != nil {
		return nil, err
	}
	if fallbackUsed {
		if err := service.SetModel(a.bootContext(), ref, targetModel, cfg.ModelSelectionIdentity(targetModel)); err != nil {
			return nil, err
		}
		a.noticeForTab(tab.ID, fmt.Sprintf("model %q is no longer available; switched to %s", requestedModel, targetModel))
	}
	a.bindControllerDisplayRecorder(candidate)
	candidate.EnableInteractiveApproval()
	runtime := snap.normalizedRuntime()
	applyTabToolApprovalModeToController(candidate, runtime.toolApprovalMode)
	applyTabQualityFloorToController(candidate, runtime.qualityFloor)
	runtime.collaborationMode = "normal"
	runtime.legacyGoal = ""
	if candidate.PlanMode() {
		runtime.collaborationMode = "plan"
	} else if candidate.GoalStatus() == control.GoalStatusRunning && strings.TrimSpace(candidate.Goal()) != "" {
		runtime.collaborationMode = "goal"
		runtime.legacyGoal = strings.TrimSpace(candidate.Goal())
	}

	a.mu.Lock()
	if len(navigationSequence) > 0 && navigationSequence[0] != 0 && a.desktopSessions.navigationSeq.Load() != navigationSequence[0] {
		a.mu.Unlock()
		return nil, errSessionNavigationSuperseded
	}
	if tab.removed || a.tabs[tab.ID] != tab || tab.Ctrl != current {
		a.mu.Unlock()
		return nil, fmt.Errorf("tab runtime changed while opening session")
	}
	if err := a.authorizeTabReplacementLocked(tab, candidate, "opening session", "session-open"); err != nil {
		a.mu.Unlock()
		return nil, err
	}
	if !a.commitSessionRuntimePathLocked(transition) {
		a.mu.Unlock()
		return nil, fmt.Errorf("tab runtime changed while opening session")
	}
	tab.Ctrl = candidate
	tab.SessionID = ref.SessionID
	tab.SessionPath = ""
	tab.model = targetModel
	tab.Label = candidate.Label()
	applyNormalizedRuntimeToTabLocked(tab, runtime)
	tab.Ready = true
	clearTabStartupError(tab)
	a.bindSessionRuntimeKeyLocked(tab, tab.currentSessionIdentity())
	a.supersedeTabBuildLocked(tab)
	a.saveTabsLocked()
	epoch := a.advanceSessionRuntimeEpochLocked(tab)
	committed = true
	a.mu.Unlock()

	retireReplacedController(current, candidate)
	discard = false
	a.notifyTabRuntimeRebuiltAtEpoch(tab, epoch)
	return candidate, nil
}
