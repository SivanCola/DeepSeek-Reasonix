package main

import (
	"context"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/sessioncatalog"
)

// resolveCanonicalSessionPath returns a unique adopted/canonical leaf for the
// topic that owns path, when the catalog has one. Empty means keep path.
// Retarget happens before Controller create/rebind so the new controller leases
// and binds authority on the canonical path only.
func (a *App) resolveCanonicalSessionPath(path string) string {
	if a == nil || strings.TrimSpace(path) == "" {
		return ""
	}
	catalog := a.sessionCatalog.Load()
	if catalog == nil {
		return ""
	}
	ctx := context.Background()
	rec, ok, err := catalog.GetSession(ctx, path)
	if err != nil || !ok {
		return ""
	}
	if rec.TopicID == "" {
		// Group by recovery group when topic is unset.
		if rec.RecoveryCanonical && (rec.RecoveryRole == sessioncatalog.RecoveryRoleAdopted || rec.RecoveryRole == sessioncatalog.RecoveryRolePreferred) {
			return ""
		}
		return ""
	}
	topic, ok, err := catalog.GetTopic(ctx, sessioncatalog.TopicKey{Scope: rec.Scope, WorkspaceRoot: rec.WorkspaceRoot, TopicID: rec.TopicID})
	if err != nil || !ok {
		return ""
	}
	return sessioncatalog.CanonicalSessionPathForTopic(topic.Sessions, path)
}

func (a *App) continuePathForOpen(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	catalog := a.sessionCatalog.Load()
	if catalog == nil {
		return ""
	}
	ctx := context.Background()
	rec, ok, err := catalog.GetSession(ctx, path)
	if err != nil || !ok {
		return a.continuePathForMissingParent(ctx, catalog, path)
	}
	if rec.TopicID == "" {
		return ""
	}
	topic, ok, err := catalog.GetTopic(ctx, sessioncatalog.TopicKey{Scope: rec.Scope, WorkspaceRoot: rec.WorkspaceRoot, TopicID: rec.TopicID})
	if err != nil || !ok {
		return ""
	}
	return sessioncatalog.OrdinaryContinuePath(topic.Sessions, path)
}

func (a *App) continuePathForMissingParent(ctx context.Context, catalog *sessioncatalog.Catalog, path string) string {
	parentID := agent.BranchID(path)
	if parentID == "" {
		return ""
	}
	// desktop-tabs.json may still name a parent that lineage folded off the
	// ordinary row. Look up the topic by the filename id.
	for _, target := range a.sessionCatalogTargets() {
		page, err := catalog.ListTopics(ctx, sessioncatalog.TopicPageRequest{
			Scope: target.Scope, WorkspaceRoot: target.WorkspaceRoot, Limit: sessioncatalog.MaxLimit,
		})
		if err != nil {
			continue
		}
		for _, topic := range page.Items {
			for _, session := range topic.Sessions {
				if session.ParentID == parentID || strings.TrimSpace(session.RecoveryGroupID) == parentID {
					if next := sessioncatalog.OrdinaryContinuePath(topic.Sessions, path); next != "" {
						return next
					}
				}
			}
		}
	}
	return ""
}

func (a *App) retargetOpenTabsToCoveringLeaves() {
	if a == nil {
		return
	}
	type pending struct {
		tab  *WorkspaceTab
		next string
	}
	a.mu.RLock()
	items := make([]pending, 0, len(a.tabs)+len(a.detachedSessions))
	collect := func(tab *WorkspaceTab) {
		if tab == nil {
			return
		}
		current := tab.currentSessionPath()
		next := a.continuePathForOpen(current)
		if next == "" || sessionRuntimeKey(next) == sessionRuntimeKey(current) {
			return
		}
		items = append(items, pending{tab: tab, next: next})
	}
	for _, tab := range a.tabs {
		collect(tab)
	}
	for _, tab := range a.detachedSessions {
		collect(tab)
	}
	a.mu.RUnlock()
	for _, item := range items {
		if item.tab.Ctrl == nil {
			a.mu.Lock()
			if a.tabs[item.tab.ID] == item.tab || a.detachedSessions[sessionRuntimeKey(item.tab.currentSessionPath())] == item.tab {
				item.tab.SessionPath = item.next
				a.saveTabsLocked()
			}
			a.mu.Unlock()
			continue
		}
		if err := a.rebindTabToSessionPath(item.tab, item.next); err != nil {
			continue
		}
	}
}
