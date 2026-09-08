package main

import (
	"reasonix/internal/config"
	"reasonix/internal/event"
)

func (a *App) loadStartupConfigWithUpgradeNotice(changed bool, upgradeErr error) (*config.Config, error) {
	cfg, err := config.Load()
	a.queueStartupConfigNotice(cfg, changed, upgradeErr)
	return cfg, err
}

// Startup runs the migration before any tab builds. Keep its result until the
// active tab has restored its display history so boot cannot swallow the notice.
func (a *App) queueStartupConfigNotice(cfg *config.Config, changed bool, err error) {
	var notice *event.Event
	if err != nil {
		notice = &event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: "Configuration upgrade did not complete. Open model settings or check the configuration file permissions, then retry.", Detail: err.Error()}
	} else if changed {
		if summary := cfg.OpenCodeGoUpgradeSummary(); summary != "" {
			notice = &event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "OpenCode Go configuration was upgraded.", Detail: summary}
		}
	}
	a.mu.Lock()
	a.startupConfigNotice = notice
	a.mu.Unlock()
}

func (a *App) takeStartupConfigNotice(tab *WorkspaceTab) (*event.Event, *tabEventSink) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.startupReady.Load() || tab == nil || tab.removed || tab.ID != a.activeTabID || tab.sink == nil {
		return nil, nil
	}
	notice := a.startupConfigNotice
	a.startupConfigNotice = nil
	return notice, tab.sink
}

// The first renderer heartbeat may follow the first controller build. Keep the
// notice pending until both exist instead of emitting before React subscribes.
func (a *App) deliverStartupConfigNoticeAfterFrontendReady() {
	a.mu.RLock()
	tab := a.tabs[a.activeTabID]
	ready := tab != nil && (tab.Ready || tab.StartupErr != "")
	a.mu.RUnlock()
	if ready {
		if notice, sink := a.takeStartupConfigNotice(tab); notice != nil {
			sink.Emit(*notice)
		}
	}
}

func (a *App) deliverStartupConfigNotice(tab *WorkspaceTab, buildGeneration uint64) {
	if !a.tabBuildSuperseded(tab, buildGeneration) {
		if notice, sink := a.takeStartupConfigNotice(tab); notice != nil {
			sink.Emit(*notice)
		}
	}
}
