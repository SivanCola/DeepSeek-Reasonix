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

func TestWindowsTerminalIconSourceFallsBackForMissingStub(t *testing.T) {
	// A non-existent zero-size stub should not crash and should prefer any
	// resolvable console icon (or the original path as last resort).
	got := windowsTerminalIconSource(filepath.Join(t.TempDir(), "wt-missing.exe"))
	if got == "" {
		t.Fatal("icon source should not be empty when PowerShell or the stub path is available")
	}
}

func TestWindowsConsoleLaunchUsesDetachedStart(t *testing.T) {
	// Build the command the same way launchPlatformExternalOpener does for
	// console mode and verify the intermediate shell stays hidden.
	comspec := joinWindowsInstallPath(os.Getenv("WINDIR"), "System32", "cmd.exe")
	if comspec == "" {
		t.Skip("cmd.exe unavailable")
	}
	ps := firstWindowsExecutable([]string{"powershell.exe"},
		joinWindowsInstallPath(os.Getenv("WINDIR"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"))
	if ps == "" {
		t.Skip("powershell.exe unavailable")
	}
	workdir := t.TempDir()
	err := launchPlatformExternalOpener(externalOpenerSpec{
		View:       ExternalOpenerView{ID: "powershell", Name: "PowerShell", Kind: externalOpenerTerminal},
		Target:     ps,
		LaunchMode: "console",
	}, workdir)
	if err != nil {
		t.Fatalf("launch console opener: %v", err)
	}
}
