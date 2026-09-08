package main

import (
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
	if err := boot.ValidateReasoningSnapshot(cfg, boot.Options{Model: snap.model, EffortOverride: snap.effort}); err != nil {
		return err
	}
	return ctrl.Snapshot()
}

func (a *App) resolveTabEffortChange(tabID, level string) (string, string, *config.Config, error) {
	entry, cfg, err := a.currentProviderEntryAndConfigForTab(tabID)
	if err != nil {
		return "", "", cfg, err
	}
	ref := entry.Name + "/" + entry.Model
	effort, err := config.NormalizeEffort(entry, level)
	if err != nil {
		return "", "", cfg, err
	}
	if err := boot.ValidateReasoningSnapshot(cfg, boot.Options{Model: ref, EffortOverride: &effort}); err != nil {
		return "", "", cfg, err
	}
	return ref, effort, cfg, nil
}
