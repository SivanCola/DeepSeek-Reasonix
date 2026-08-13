package main

// preferLiveSessionPath chooses which session a topic row should open.
// A live runtime wins over the catalog representative. An explicit
// non-representative path (History inspect) is left alone.
func preferLiveSessionPath(requested, live, representative string) string {
	liveKey := sessionRuntimeKey(live)
	if liveKey == "" {
		return requested
	}
	reqKey := sessionRuntimeKey(requested)
	if reqKey == "" || reqKey == liveKey {
		if reqKey == "" {
			return live
		}
		return requested
	}
	repKey := sessionRuntimeKey(representative)
	if repKey == "" || reqKey == repKey {
		return live
	}
	return requested
}

func (a *App) liveSessionPathForTopic(scope, workspaceRoot, topicID string) string {
	if a == nil {
		return ""
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if tab := a.liveRuntimeForTopicLocked(scope, workspaceRoot, topicID); tab != nil {
		return tab.currentSessionPath()
	}
	return ""
}

func (a *App) liveRuntimeForTopicLocked(scope, workspaceRoot, topicID string) *WorkspaceTab {
	var best *WorkspaceTab
	visit := func(tab *WorkspaceTab) {
		if tab == nil || !tabMatchesTopicTarget(tab, scope, workspaceRoot, topicID) {
			return
		}
		if tab.currentSessionPath() == "" {
			return
		}
		if best == nil {
			best = tab
			return
		}
		if best.Ctrl == nil && tab.Ctrl != nil {
			best = tab
			return
		}
		if normalizeTopicStatus(best.ActivityStatus) == "" && normalizeTopicStatus(tab.ActivityStatus) != "" {
			best = tab
		}
	}
	for _, tab := range a.tabs {
		visit(tab)
	}
	for _, tab := range a.detachedSessions {
		visit(tab)
	}
	return best
}

func (a *App) resolveOpenSessionPath(scope, workspaceRoot, topicID, requested string) string {
	live := a.liveSessionPathForTopic(scope, workspaceRoot, topicID)
	representative := a.catalogSessionPathForTopic(scope, workspaceRoot, topicID)
	return preferLiveSessionPath(requested, live, representative)
}

func (a *App) openTopicTabPreferLive(scope, workspaceRoot, topicID, sessionPath string) (TabMeta, error) {
	return a.openTopicTabPreferLiveActivation(scope, workspaceRoot, topicID, sessionPath, true)
}

func (a *App) openTopicTabPreferLiveInactive(scope, workspaceRoot, topicID, sessionPath string) (TabMeta, error) {
	return a.openTopicTabPreferLiveActivation(scope, workspaceRoot, topicID, sessionPath, false)
}

func (a *App) openTopicTabPreferLiveActivation(scope, workspaceRoot, topicID, sessionPath string, activate bool) (TabMeta, error) {
	sessionPath = a.resolveOpenSessionPath(scope, workspaceRoot, topicID, sessionPath)
	a.mu.Lock()
	if promoted := a.promoteDetachedRuntimeLocked(sessionPath, activate); promoted != nil {
		meta := a.tabMeta(promoted, promoted.ID == a.activeTabID)
		a.saveTabsLocked()
		a.mu.Unlock()
		return enrichTabMeta(meta), nil
	}
	a.mu.Unlock()
	return a.openTopicTabWithActivation(scope, workspaceRoot, topicID, sessionPath, activate)
}

func (a *App) promoteDetachedRuntimeLocked(sessionPath string, activate bool) *WorkspaceTab {
	key := sessionRuntimeKey(sessionPath)
	if key == "" {
		return nil
	}
	tab := a.detachedSessions[key]
	if tab == nil || tab.Ctrl == nil {
		return nil
	}
	delete(a.detachedSessions, key)
	if a.tabs[tab.ID] == nil {
		a.tabs[tab.ID] = tab
		a.tabOrder = append(a.tabOrder, tab.ID)
	}
	if activate {
		a.activeTabID = tab.ID
	}
	return tab
}
