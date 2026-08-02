package main

import (
	"encoding/json"
	"fmt"
	"os"

	"reasonix/internal/config"
	"reasonix/internal/repair"
)

// ConfigRepairView is the recovery banner payload: what was repaired, when,
// and what the user can do about it.
type ConfigRepairView struct {
	Outcome     string `json:"outcome"` // "auto_fixed" | "restored_snapshot" | "safe_mode" | ""
	Scope       string `json:"scope"`   // "global" | "project"
	Path        string `json:"path"`
	Detail      string `json:"detail"`
	RepairedAt  string `json:"repairedAt"`
	Undoable    bool   `json:"undoable"`
	CanOpenFile bool   `json:"canOpenFile"`
}

// ConfigRepairStatus returns the recovery banner payload:
//
//   - a "config_damaged" view while the user-global config fails to load and
//     the app runs on the recovery configuration (persistent until fixed);
//   - a consume-once "auto_fixed" view when the Guard or startup recovery
//     repaired the global config at this launch.
func (a *App) ConfigRepairStatus() ConfigRepairView {
	a.mu.RLock()
	damaged := a.globalConfigDamaged
	a.mu.RUnlock()
	if !damaged {
		// Live check: the banner must appear even before the first tab build
		// records the failure, and must disappear once the config is fixed.
		if _, err := config.LoadUserConfigReadOnly(); err != nil {
			damaged = true
			a.mu.Lock()
			a.globalConfigDamaged = true
			a.mu.Unlock()
		}
	}
	if damaged {
		return ConfigRepairView{
			Outcome:     "config_damaged",
			Scope:       "global",
			Path:        config.UserConfigPath(),
			Detail:      "全局配置已损坏，Reasonix 已使用内置安全配置启动；外部集成保持禁用",
			Undoable:    false,
			CanOpenFile: true,
		}
	}
	view := ConfigRepairView{Outcome: ""}
	if markerPath := repair.ConfigEscapeRepairMarkerPath(); markerPath != "" {
		b, err := os.ReadFile(markerPath)
		if err == nil {
			var marker repair.ConfigEscapeRepairMarker
			if json.Unmarshal(b, &marker) == nil && marker.SchemaVersion == 1 && marker.Path != "" {
				view = ConfigRepairView{
					Outcome:     "auto_fixed",
					Scope:       marker.Scope,
					Path:        marker.Path,
					Detail:      fmt.Sprintf("%d Windows path(s) repaired", marker.FixedCount),
					RepairedAt:  marker.RepairedAt,
					Undoable:    marker.TransactionID != "",
					CanOpenFile: true,
				}
			}
			_ = os.Remove(markerPath)
		}
	}
	return view
}

// UndoConfigRepair reverts the most recent repair transaction (restoring the
// original config bytes), so the user can review a repaired value and undo it.
func (a *App) UndoConfigRepair() error {
	tx, err := repair.UndoLastRepair()
	if err != nil {
		return err
	}
	if tx == nil {
		return fmt.Errorf("no repair to undo")
	}
	return nil
}

// OpenConfigFile reveals the global config file in the platform file manager.
func (a *App) OpenConfigFile() error {
	path := config.UserConfigPath()
	if path == "" {
		return fmt.Errorf("user config path is unavailable")
	}
	return revealPath(path)
}
