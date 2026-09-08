package main

import (
	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/control"
)

// Reject an invalid role before correcting a restored workspace binding retires
// its working runtime. Return the typed cause instead of StartupErr text.
func (a *App) snapshotTabWorkspaceForRebuild(tab *WorkspaceTab, ctrl control.SessionAPI, root string) error {
	cfg, err := config.LoadForRoot(root)
	if err != nil {
		return err
	}
	snap := a.tabRuntimeSnapshot(tab)
	if model, identity, ok := agent.LoadSessionModelSelection(ctrl.SessionPath()); ok {
		resolved, err := cfg.ResolveSavedModel(model, identity)
		if err != nil {
			return err
		}
		snap.model = resolved
	}
	if err := boot.ValidateReasoningSnapshot(cfg, boot.Options{Model: snap.model, EffortOverride: snap.effort}); err != nil {
		return err
	}
	return ctrl.Snapshot()
}

func controllerModelSelectionIdentity(ctrl control.SessionAPI) string {
	if selected, ok := ctrl.(interface{ ModelSelectionIdentity() string }); ok {
		return selected.ModelSelectionIdentity()
	}
	return ""
}

func (a *App) resolveTabEffortChange(tabID, level string) (string, string, *config.Config, error) {
	entry, cfg, err := a.currentProviderEntryAndConfigForTab(tabID)
	if err != nil {
		return "", "", cfg, err
	}
	ref := entry.Name + "/" + entry.Model
	if ctrl := a.controllerForTab(a.tabByID(tabID)); ctrl != nil {
		if _, err := cfg.ResolveSavedModel(ref, controllerModelSelectionIdentity(ctrl)); err != nil {
			return "", "", cfg, err
		}
	}
	effort, err := config.NormalizeEffort(entry, level)
	if err != nil {
		return "", "", cfg, err
	}
	if err := boot.ValidateReasoningSnapshot(cfg, boot.Options{Model: ref, EffortOverride: &effort}); err != nil {
		return "", "", cfg, err
	}
	return ref, effort, cfg, nil
}
