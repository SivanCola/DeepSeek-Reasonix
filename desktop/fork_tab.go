package main

import (
	"fmt"
	"strings"

	"reasonix/internal/agent"
)

// openForkedSessionTab attaches an already-written fork session to a new tab.
// The source tab keeps its controller and transcript. The fork becomes active
// only while the source tab still owns focus.
func (a *App) openForkedSessionTab(sourceTab *WorkspaceTab, newPath string) (TabMeta, error) {
	if sourceTab == nil || strings.TrimSpace(newPath) == "" {
		return TabMeta{}, fmt.Errorf("fork tab needs a source tab and session path")
	}
	a.mu.RLock()
	if a.tabs[sourceTab.ID] != sourceTab {
		a.mu.RUnlock()
		return TabMeta{}, nil
	}
	scope := sourceTab.Scope
	workspaceRoot := sourceTab.WorkspaceRoot
	sourceTitle := sourceTab.TopicTitle
	model := sourceTab.model
	effort := cloneStringPtr(sourceTab.effort)
	mode := currentTabMode(sourceTab)
	toolApprovalMode := currentTabToolApprovalMode(sourceTab)
	disabledMCP := cloneServerViewMap(sourceTab.disabledMCP)
	mcpOrder := append([]string(nil), sourceTab.mcpOrder...)
	a.mu.RUnlock()

	topicID := newTopicID()
	topicTitle := a.forkTopicTitle(sourceTitle)
	titleRoot := workspaceRoot
	if scope == "global" {
		titleRoot = ""
	}
	if err := setTopicTitle(titleRoot, topicID, topicTitle); err != nil {
		return TabMeta{}, err
	}
	m, _ := agent.EnsureBranchMeta(newPath)
	m.Scope = scope
	m.WorkspaceRoot = workspaceRoot
	m.TopicID = topicID
	m.TopicTitle = topicTitle
	if err := agent.SaveBranchMeta(newPath, m); err != nil {
		return TabMeta{}, err
	}
	invalidateTopicSessionIndexForPath(newPath)

	a.mu.Lock()
	if a.tabs[sourceTab.ID] != sourceTab {
		a.mu.Unlock()
		return TabMeta{}, nil
	}
	newTabID := a.newUniqueTabIDLocked()
	tab := &WorkspaceTab{
		ID:               newTabID,
		Scope:            scope,
		WorkspaceRoot:    workspaceRoot,
		TopicID:          topicID,
		TopicTitle:       topicTitle,
		topicTitleSource: topicTitleSourceManual,
		SessionPath:      newPath,
		model:            model,
		effort:           effort,
		mode:             mode,
		toolApprovalMode: toolApprovalMode,
		disabledMCP:      disabledMCP,
		mcpOrder:         mcpOrder,
	}
	tab.sink = &tabEventSink{tabID: newTabID, app: a}
	a.tabs[newTabID] = tab
	a.tabOrder = append(a.tabOrder, newTabID)
	activateFork := a.activeTabID == sourceTab.ID
	if activateFork {
		a.activeTabID = newTabID
	}
	a.saveTabsLocked()
	meta := a.tabMeta(tab, activateFork)
	a.mu.Unlock()

	a.emitProjectTreeChangedForSessionDirs(sessionDirectoryForPath(newPath))
	a.startTabControllerBuild(tab)
	return meta, nil
}
