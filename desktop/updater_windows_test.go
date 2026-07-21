//go:build windows

package main

import "testing"

func TestInstallerCommandShowsUpdateProgressAndPassesUnquotedDFlagLast(t *testing.T) {
	cmd := installerCommand(`C:\Temp\reasonix-update-1.exe`, `D:\Tools\Reasonix App`)
	if cmd.SysProcAttr == nil {
		t.Fatal("expected a raw command line forcing the install dir")
	}
	got := cmd.SysProcAttr.CmdLine
	want := `"C:\Temp\reasonix-update-1.exe" /REASONIXUPDATE=1 /D=D:\Tools\Reasonix App`
	if got != want {
		t.Fatalf("CmdLine = %q, want %q", got, want)
	}
	if cmd.SysProcAttr.HideWindow {
		t.Fatal("NSIS update progress window must remain visible")
	}
}

func TestInstallerCommandWithoutDirSkipsDFlag(t *testing.T) {
	cmd := installerCommand(`C:\Temp\reasonix-update-1.exe`, "")
	if cmd.SysProcAttr == nil {
		t.Fatal("expected a raw command line for visible updater installs")
	}
	got := cmd.SysProcAttr.CmdLine
	want := `"C:\Temp\reasonix-update-1.exe" /REASONIXUPDATE=1`
	if got != want {
		t.Fatalf("CmdLine = %q, want %q", got, want)
	}
}
