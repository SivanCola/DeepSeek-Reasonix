package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/repair"
)

// TabConfigErrorView surfaces a workspace's broken reasonix.toml: the file,
// the parse location, and a ready-to-confirm repair preview when the damage
// is a high-confidence Windows-path escape issue.
type TabConfigErrorView struct {
	Path       string `json:"path"`
	FileName   string `json:"fileName"`
	Line       int    `json:"line,omitempty"`
	Column     int    `json:"column,omitempty"`
	Message    string `json:"message"`
	FixCount   int    `json:"fixCount,omitempty"`
	HasPreview bool   `json:"hasPreview,omitempty"`
}

// isGlobalConfigFile reports whether a failing config path belongs to the
// user-global configuration (as opposed to a workspace's reasonix.toml).
func isGlobalConfigFile(path string) bool {
	path = filepath.Clean(path)
	if user := config.UserConfigPath(); user != "" && filepath.Clean(user) == path {
		return true
	}
	if memory := config.MemoryUserDir(); memory != "" {
		memory = filepath.Clean(memory)
		if path == memory || strings.HasPrefix(path, memory+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// setTabConfigError records the workspace's project-config failure with a
// repair preview. Only the affected tab degrades; other workspaces continue.
func (a *App) setTabConfigError(tab *WorkspaceTab, cle *config.ConfigLoadError, root string) {
	view := &TabConfigErrorView{
		Path:     cle.Path,
		FileName: filepath.Base(cle.Path),
		Line:     cle.Line,
		Message:  cle.Err.Error(),
	}
	// A high-confidence Windows-path escape repair is offered as a preview.
	if fixes, err := scanProjectConfigEscapes(cle.Path); err == nil && len(fixes) > 0 {
		view.FixCount = len(fixes)
		view.HasPreview = true
	}
	a.mu.Lock()
	tab.ConfigError = view
	a.mu.Unlock()
}

func (a *App) clearTabConfigError(tab *WorkspaceTab) {
	a.mu.Lock()
	tab.ConfigError = nil
	a.mu.Unlock()
}

// scanProjectConfigEscapes reads the project config and produces the repair
// preview (never writes).
func scanProjectConfigEscapes(path string) ([]config.TOMLEscapeFix, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return config.ScanTOMLPathEscapes(string(b))
}

// ApplyProjectConfigFix applies the confirmed Windows-path escape repair to
// the workspace's reasonix.toml. The state captured when the preview was
// generated is re-verified before writing, and the change is undoable.
func (a *App) ApplyProjectConfigFix(tabID string) error {
	tab := a.tabByID(tabID)
	if tab == nil {
		return fmt.Errorf("tab %q is no longer open", tabID)
	}
	a.mu.RLock()
	view := tab.ConfigError
	root := tab.WorkspaceRoot
	a.mu.RUnlock()
	if view == nil || !view.HasPreview {
		return fmt.Errorf("no config repair preview for this workspace")
	}
	project := projectConfigPath(root)
	fixes, err := scanProjectConfigEscapes(project)
	if err != nil {
		return err
	}
	if len(fixes) == 0 {
		return fmt.Errorf("config already repaired")
	}
	expected := map[string]string{project: repairStateIDFor(project)}
	report, err := repair.ApplyConfigEscapes(repair.ConfigEscapesOptions{Root: root, IncludeProject: true, ExpectedStates: expected})
	if err != nil {
		return err
	}
	if !report.Project.Applied {
		return fmt.Errorf("config repair was not applied; the file changed since the preview")
	}
	a.clearTabConfigError(tab)
	return nil
}

// OpenProjectConfigFile reveals the workspace's reasonix.toml in the platform
// file manager.
func (a *App) OpenProjectConfigFile(tabID string) error {
	tab := a.tabByID(tabID)
	if tab == nil {
		return fmt.Errorf("tab %q is no longer open", tabID)
	}
	a.mu.RLock()
	root := tab.WorkspaceRoot
	a.mu.RUnlock()
	path := projectConfigPath(root)
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return revealPath(path)
}

// RestoreGlobalConfigSnapshot restores the user config from the last-known-
// good snapshot through the repair transaction machinery (the damaged file is
// quarantined and undoable) and returns whether a restore happened.
func (a *App) RestoreGlobalConfigSnapshot() (bool, error) {
	if config.UserConfigPath() == "" {
		return false, fmt.Errorf("user config path is unavailable")
	}
	report, err := repair.InspectAndRepairConfig(repair.ConfigOptions{Apply: true, OnlyScope: "global"})
	if err != nil {
		return false, err
	}
	restored := false
	for _, check := range report.Checks {
		if check.Scope == "global" && check.Valid && check.Exists {
			restored = true
		}
	}
	if !restored && len(report.Applied) == 0 {
		return false, fmt.Errorf("no healthy snapshot is available")
	}
	a.mu.Lock()
	a.globalConfigDamaged = false
	a.mu.Unlock()
	return true, nil
}

func projectConfigPath(root string) string {
	if root == "" || root == "." {
		return "reasonix.toml"
	}
	return filepath.Join(root, "reasonix.toml")
}

func repairStateIDFor(path string) string {
	// The repair package derives state IDs from the file; reuse the same
	// binding so ApplyConfigEscapes verifies the preview state exactly.
	return repair.FileStateID(path)
}
