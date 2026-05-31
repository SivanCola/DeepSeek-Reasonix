package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShimUsesDesktopCliMode(t *testing.T) {
	shim := generateShim("/Applications/Reasonix.app/Contents/MacOS/reasonix-desktop")
	if !strings.HasPrefix(shim, "#!/bin/sh\n") {
		t.Fatalf("shim missing shebang: %q", shim)
	}
	if !strings.Contains(shim, managedMarker) {
		t.Fatalf("shim missing marker: %q", shim)
	}
	if !strings.Contains(shim, " --cli \"$@\"") {
		t.Fatalf("shim does not forward through --cli: %q", shim)
	}
}

func TestShellQuotePreventsExpansion(t *testing.T) {
	if got := shellQuote("/tmp/Reasonix $HOME`whoami\""); got != "'/tmp/Reasonix $HOME`whoami\"'" {
		t.Fatalf("shellQuote expansion case = %q", got)
	}
	if got := shellQuote("/tmp/Reasonix's app"); got != "'/tmp/Reasonix'\\''s app'" {
		t.Fatalf("shellQuote single quote case = %q", got)
	}
	shim := generateShim("/tmp/Reasonix $HOME/Reasonix's binary")
	if !strings.Contains(shim, "exec '/tmp/Reasonix $HOME/Reasonix'\\''s binary' --cli \"$@\"") {
		t.Fatalf("shim path was not shell-quoted: %q", shim)
	}
}

func TestCommandStateDetectsMissingInstalledOutdatedAndForeign(t *testing.T) {
	expected := generateShim("/opt/reasonix-desktop")
	oldManaged := generateShim("/old/reasonix-desktop")
	foreign := "#!/bin/sh\necho foreign\n"
	if got := commandState(nil, expected, false); got != "missing" {
		t.Fatalf("missing state = %q", got)
	}
	if got := commandState(&expected, expected, true); got != "installed" {
		t.Fatalf("installed state = %q", got)
	}
	if got := commandState(&expected, expected, false); got != "needsUpdate" {
		t.Fatalf("non-executable state = %q", got)
	}
	if got := commandState(&oldManaged, expected, true); got != "needsUpdate" {
		t.Fatalf("outdated state = %q", got)
	}
	if got := commandState(&foreign, expected, true); got != "foreign" {
		t.Fatalf("foreign state = %q", got)
	}
}

func TestWriteAtomicUsesExclusiveSiblingTemp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "reasonix")
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("do not touch"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, ".reasonix.tmp")); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(target, "safe shim", 0o755); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "do not touch" {
		t.Fatalf("victim changed to %q", got)
	}
	if got, _ := os.ReadFile(target); string(got) != "safe shim" {
		t.Fatalf("target = %q", got)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("target mode = %v, %v", info.Mode().Perm(), err)
	}
}

func TestAdminInstallScriptWritesTrustedShimInsideElevatedShell(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "reasonix")
	shim := generateShim("/tmp/Reasonix's desktop")
	script := buildAdminInstallShellScript(shim, target)
	output, err := exec.Command("/bin/sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, output)
	}
	if got, _ := os.ReadFile(target); string(got) != shim {
		t.Fatalf("target shim = %q", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("left temp entries: %#v", entries)
	}
}
