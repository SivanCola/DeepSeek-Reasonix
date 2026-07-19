package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"reasonix/internal/config"
)

// remotePrefs is desktop-only remote UI state, stored beside the other desktop
// JSON prefs (desktop-workspaces.json, desktop-tabs.json). All fields are
// optional so an older file decodes cleanly.
type remotePrefs struct {
	LastHostID          string            `json:"lastHostId,omitempty"`
	LastWorkspaceByHost map[string]string `json:"lastWorkspaceByHost,omitempty"`
	ExplorerTab         string            `json:"explorerTab,omitempty"`
}

func remotePrefsPath() string {
	dir := config.MemoryUserDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "desktop-remote.json")
}

func loadRemotePrefs() remotePrefs {
	var p remotePrefs
	path := remotePrefsPath()
	if path == "" {
		return p
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return p
	}
	_ = json.Unmarshal(data, &p)
	if p.LastWorkspaceByHost == nil {
		p.LastWorkspaceByHost = map[string]string{}
	}
	return p
}

func saveRemotePrefs(p remotePrefs) {
	path := remotePrefsPath()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

func (a *App) saveLastRemoteWorkspace(hostID, workspace string) {
	p := loadRemotePrefs()
	if p.LastWorkspaceByHost == nil {
		p.LastWorkspaceByHost = map[string]string{}
	}
	p.LastHostID = hostID
	p.LastWorkspaceByHost[hostID] = workspace
	saveRemotePrefs(p)
}

// RemoteLastWorkspace returns the last opened workspace for hostID (bound so
// the frontend can prefill the server card).
func (a *App) RemoteLastWorkspace(hostID string) string {
	return loadRemotePrefs().LastWorkspaceByHost[hostID]
}
