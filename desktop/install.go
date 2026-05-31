package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	managedMarker = "# Managed by Reasonix Desktop - do not edit."
	targetPath    = "/usr/local/bin/reasonix"
)

type InstallResult struct {
	Path      string `json:"path"`
	Action    string `json:"action"`
	UsedAdmin bool   `json:"usedAdmin"`
}

type CommandStatus struct {
	Path  string `json:"path"`
	State string `json:"state"`
}

func isManaged(existing string) bool {
	for _, line := range strings.Split(existing, "\n") {
		if strings.TrimSpace(line) == managedMarker {
			return true
		}
	}
	return false
}

func commandState(existing *string, expected string, isExecutable bool) string {
	if existing == nil {
		return "missing"
	}
	if !isManaged(*existing) {
		return "foreign"
	}
	if *existing == expected && isExecutable {
		return "installed"
	}
	return "needsUpdate"
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func generateShim(desktopPath string) string {
	return fmt.Sprintf("#!/bin/sh\n%s\nexec %s --cli \"$@\"\n", managedMarker, shellQuote(desktopPath))
}

func expectedShim() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("desktop executable not available: %w", err)
	}
	return generateShim(exe), nil
}

func writeAtomic(path string, content string, mode os.FileMode) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", parent, err)
	}
	tmp, err := os.CreateTemp(parent, ".reasonix.*.tmp")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename tmp: %w", err)
	}
	return nil
}

func applescriptEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`)
}

func buildAdminInstallShellScript(shim string, target string) string {
	return fmt.Sprintf(
		"target=%s; "+
			"parent=$(dirname \"$target\") && "+
			"tmp= && "+
			"cleanup() { if [ -n \"$tmp\" ]; then rm -f \"$tmp\"; fi; } && "+
			"trap cleanup EXIT && "+
			"mkdir -p \"$parent\" && "+
			"if [ -e \"$target\" ] && ! grep -Fxq %s \"$target\"; then "+
			"echo 'target exists and was not installed by Reasonix Desktop' >&2; exit 77; "+
			"fi && "+
			"tmp=$(mktemp \"$parent/.reasonix.XXXXXX\") && "+
			"printf %%s %s > \"$tmp\" && "+
			"chmod 755 \"$tmp\" && "+
			"mv -f \"$tmp\" \"$target\" && "+
			"tmp=",
		shellQuote(target),
		shellQuote(managedMarker),
		shellQuote(shim),
	)
}

func tryInstallAsAdmin(shim string, action string) (InstallResult, error) {
	shellScript := buildAdminInstallShellScript(shim, targetPath)
	script := fmt.Sprintf(`do shell script "%s" with administrator privileges`, applescriptEscape(shellScript))
	output, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			stderr := string(ee.Stderr)
			if strings.Contains(stderr, "User canceled") || strings.Contains(stderr, "canceled") {
				return InstallResult{}, fmt.Errorf("permission cancelled by user")
			}
			return InstallResult{}, fmt.Errorf("admin install failed: %s", strings.TrimSpace(stderr))
		}
		return InstallResult{}, fmt.Errorf("osascript failed: %w", err)
	}
	_ = output
	return InstallResult{Path: targetPath, Action: action, UsedAdmin: true}, nil
}

func tryInstallDirect(shim string) (InstallResult, error) {
	action := "installed"
	if existing, err := os.ReadFile(targetPath); err == nil {
		if !isManaged(string(existing)) {
			return InstallResult{}, fmt.Errorf("%s already exists and was not installed by Reasonix Desktop. Remove or rename it first, then try again", targetPath)
		}
		action = "updated"
	} else if !os.IsNotExist(err) {
		return InstallResult{}, fmt.Errorf("read existing: %w", err)
	}
	if err := writeAtomic(targetPath, shim, 0o755); err != nil {
		return InstallResult{}, err
	}
	return InstallResult{Path: targetPath, Action: action}, nil
}

func shouldRetryWithAdmin(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "permission denied") ||
		strings.Contains(lower, "read-only") ||
		strings.Contains(lower, "operation not permitted") ||
		strings.Contains(lower, "not permitted")
}

func installAction() (string, error) {
	if existing, err := os.ReadFile(targetPath); err == nil {
		if !isManaged(string(existing)) {
			return "", fmt.Errorf("%s already exists and was not installed by Reasonix Desktop. Remove or rename it first, then try again", targetPath)
		}
		return "updated", nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return "installed", nil
}

func (a *App) ReasonixCommandStatus() CommandStatus {
	if runtime.GOOS != "darwin" {
		return CommandStatus{Path: targetPath, State: "unsupported"}
	}
	expected, err := expectedShim()
	if err != nil {
		return CommandStatus{Path: targetPath, State: "unsupported"}
	}
	var existing *string
	if raw, err := os.ReadFile(targetPath); err == nil {
		s := string(raw)
		existing = &s
	}
	isExecutable := false
	if info, err := os.Stat(targetPath); err == nil {
		isExecutable = info.Mode()&0o111 != 0
	}
	return CommandStatus{Path: targetPath, State: commandState(existing, expected, isExecutable)}
}

func (a *App) InstallReasonixCommand() (InstallResult, error) {
	if runtime.GOOS != "darwin" {
		return InstallResult{}, fmt.Errorf("install command is only available on macOS")
	}
	shim, err := expectedShim()
	if err != nil {
		return InstallResult{}, err
	}
	result, err := tryInstallDirect(shim)
	if err == nil {
		return result, nil
	}
	if !shouldRetryWithAdmin(err) {
		return InstallResult{}, err
	}
	action, actionErr := installAction()
	if actionErr != nil {
		return InstallResult{}, err
	}
	return tryInstallAsAdmin(shim, action)
}
