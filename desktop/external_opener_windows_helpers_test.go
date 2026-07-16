package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsTerminalIconCandidatePathsPreferPackageBinary(t *testing.T) {
	// Use fixed Windows-style roots so the assertion is OS-independent; helpers
	// only join path segments and never touch the filesystem.
	wtAlias := `C:\Users\x\AppData\Local\Microsoft\WindowsApps\wt.exe`
	local := `C:\Users\x\AppData\Local`
	programFiles := `C:\Program Files`

	got := windowsTerminalIconCandidatePaths(wtAlias, local, programFiles)
	if len(got) < 2 {
		t.Fatalf("candidates = %v, want package paths before alias", got)
	}
	wantFirst := filepath.Join(programFiles, "WindowsApps", "Microsoft.WindowsTerminal_*", "WindowsTerminal.exe")
	if got[0] != wantFirst {
		t.Fatalf("first candidate = %q, want %q", got[0], wantFirst)
	}
	// Primary hit must be the Store package under Program Files\WindowsApps, not
	// a nested path under the App Execution Alias directory.
	if !strings.Contains(got[0], "WindowsApps"+string(filepath.Separator)+"Microsoft.WindowsTerminal_") {
		t.Fatalf("first candidate = %q, want Microsoft.WindowsTerminal_* under WindowsApps", got[0])
	}
	last := got[len(got)-1]
	if last != wtAlias {
		t.Fatalf("last candidate = %q, want wt alias %q", last, wtAlias)
	}
}

func TestWindowsConsoleLaunchPlanBypassesCmdAndPreservesMetacharacters(t *testing.T) {
	cases := []struct {
		name    string
		target  string
		workdir string
	}{
		{name: "spaces", target: `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, workdir: `C:\Users\demo\My Project`},
		{name: "ampersand", target: `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, workdir: `C:\src\repo&calc`},
		{name: "pipe_parens", target: `C:\Windows\System32\cmd.exe`, workdir: `C:\src\a|b(c)^%d`},
		{name: "command_prompt_is_direct_target", target: `C:\Windows\System32\cmd.exe`, workdir: `D:\work`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := planWindowsConsoleLaunch(tc.target, tc.workdir)
			if !windowsConsoleLaunchIsDirect(plan) {
				t.Fatalf("plan is not a direct ShellExecute open: %+v", plan)
			}
			if plan.File != tc.target {
				t.Fatalf("File = %q, want %q (target binary, not cmd /c start wrapper)", plan.File, tc.target)
			}
			if plan.Dir != tc.workdir {
				t.Fatalf("Dir = %q, want exact workdir (no shell rewriting)", plan.Dir)
			}
		})
	}
}

func TestWindowsConsoleLaunchIsDirectRejectsEmptyFile(t *testing.T) {
	if windowsConsoleLaunchIsDirect(windowsConsoleLaunch{}) {
		t.Fatal("empty plan must not count as direct")
	}
}
