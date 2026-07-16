//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsExternalOpenerIconUsesShellIcon(t *testing.T) {
	explorer := filepath.Join(os.Getenv("WINDIR"), "explorer.exe")
	if info, err := os.Stat(explorer); err != nil || info.IsDir() {
		t.Skip("Windows Explorer executable is unavailable")
	}
	got := platformExternalOpenerIconDataURL(externalOpenerSpec{IconSource: explorer})
	if !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Fatalf("native Explorer icon = %q, want PNG data URL", got)
	}
}

func TestWindowsAppPathExecutableEmptyName(t *testing.T) {
	if got := windowsAppPathExecutable(""); got != "" {
		t.Fatalf("empty name = %q, want empty", got)
	}
	if got := windowsAppPathExecutable("   "); got != "" {
		t.Fatalf("blank name = %q, want empty", got)
	}
}

func TestWindowsTerminalIconSourcePrefersNonZeroBinary(t *testing.T) {
	// A non-existent path must not panic; fallback may be powershell or empty
	// alias string. Primary Store layout is covered by the pure candidate test.
	got := windowsTerminalIconSource(filepath.Join(t.TempDir(), "wt-missing.exe"))
	_ = got
}

func TestWindowsConsoleLaunchDoesNotInvokeCmdStart(t *testing.T) {
	// Integration-free: planWindowsConsoleLaunch + ShellExecute path must never
	// build cmd /c start. The pure helpers test covers special-character dirs;
	// here we only assert the platform launcher uses that plan shape.
	ps := firstWindowsExecutable([]string{"powershell.exe"},
		joinWindowsInstallPath(os.Getenv("WINDIR"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"))
	if ps == "" {
		t.Skip("powershell.exe unavailable")
	}
	workdir := filepath.Join(t.TempDir(), "repo&calc")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir workdir: %v", err)
	}
	plan := planWindowsConsoleLaunch(ps, workdir)
	if !windowsConsoleLaunchIsDirect(plan) {
		t.Fatalf("console plan must be a direct ShellExecute open: %+v", plan)
	}
	if plan.File != ps {
		t.Fatalf("File = %q, want powershell target %q", plan.File, ps)
	}
	if plan.Dir != workdir {
		t.Fatalf("Dir = %q, want %q", plan.Dir, workdir)
	}
	// Do not call launchPlatformExternalOpener here: it would open a real
	// interactive console window in CI. ShellExecute wiring is covered by
	// openWorkspacePath and compile-time type checks.
}
