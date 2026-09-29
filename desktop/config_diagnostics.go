package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/servecontract"
)

// ConfigDiagnostics resolves an opaque tab binding. No caller-supplied path is
// used to load configuration, and remote failures never fall back to local IO.
func (a *App) ConfigDiagnostics(tabID string) config.DiagnosticSnapshot {
	view := a.configDiagnostics(tabID, "")
	if a.ctx != nil {
		a.runtimeEvents.Emit(a.ctx, "config:diagnostics", view)
	}
	return view
}

// ConfigDiagnosticDetails is user-initiated; background snapshots and exports
// deliberately omit historical command bodies.
func (a *App) ConfigDiagnosticDetails(tabID, diagnosticID string) config.DiagnosticSnapshot {
	return a.configDiagnostics(tabID, diagnosticID)
}

func (a *App) configDiagnostics(tabID, detailID string) config.DiagnosticSnapshot {
	a.remoteTabMu.Lock()
	remote := a.remoteTabs[tabID]
	if remote != nil {
		host, root := remote.ref.HostID, remote.ref.Workspace
		client, base, route, gen := remote.client, remote.base, remote.routing.currentPath, remote.gen
		supported := remote.capabilities[servecontract.ConfigDiagnosticsV1]
		a.remoteTabMu.Unlock()
		view := config.NewDiagnosticSnapshot(host, root, "unsupported")
		if !supported {
			return view
		}
		view.Status = "unavailable"
		if client == nil || route == "" {
			return view
		}
		ctx, cancel := context.WithTimeout(a.bootContext(), 10*time.Second)
		defer cancel()
		endpoint := sessionExportURL(base, "/config-diagnostics", route, false)
		if detailID != "" {
			endpoint += "&detail=" + url.QueryEscape(detailID)
		}
		resp, err := serveDoForSession(ctx, client, http.MethodGet, endpoint, nil, route)
		if err != nil {
			return view
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return view
		}
		var result config.DiagnosticSnapshot
		if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result) != nil {
			return view
		}
		a.remoteTabMu.Lock()
		current := a.remoteTabs[tabID]
		valid := current == remote && remote.client == client && remote.gen == gen && remote.routing.currentPath == route && remote.ref.Workspace == root
		a.remoteTabMu.Unlock()
		if !valid {
			return view
		}
		result.HostID = host // Desktop connection identity, not the service's local alias.
		if result.Items == nil {
			result.Items = []config.Diagnostic{}
		}
		return result
	}
	a.remoteTabMu.Unlock()
	a.mu.RLock()
	tab := a.tabByIDLocked(tabID)
	if tab == nil || tab.removed {
		a.mu.RUnlock()
		return config.NewDiagnosticSnapshot(localDesktopHostID, "", "unavailable")
	}
	root, generation := tab.WorkspaceRoot, tab.SessionGeneration
	if root == "" && tab.Ctrl != nil {
		root = tab.Ctrl.WorkspaceRoot()
	}
	a.mu.RUnlock()
	view := config.InspectDiagnosticDetails(localDesktopHostID, root, detailID)
	a.mu.RLock()
	valid := a.tabByIDLocked(tabID) == tab && !tab.removed && tab.SessionGeneration == generation && (tab.WorkspaceRoot == root || tab.WorkspaceRoot == "")
	a.mu.RUnlock()
	if !valid {
		return config.NewDiagnosticSnapshot(localDesktopHostID, root, "unavailable")
	}
	return view
}

// OpenConfigDiagnosticSource re-resolves the issue instead of accepting an
// arbitrary filesystem path from the renderer.
func (a *App) OpenConfigDiagnosticSource(tabID, diagnosticID string) error {
	view := a.ConfigDiagnostics(tabID)
	if view.HostID != localDesktopHostID {
		return fmt.Errorf("open the configuration on its remote host")
	}
	for _, item := range view.Items {
		if item.ID != diagnosticID || item.Source == "" {
			continue
		}
		path := item.Source
		if _, err := os.Stat(path); err != nil {
			path = filepath.Dir(path)
		}
		return a.RevealPath(path)
	}
	return fmt.Errorf("configuration diagnostic changed; reload its details")
}
