package main

import (
	"context"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows"
	"reasonix/internal/proc"
)

func scriptCommand(ctx context.Context, script string, extra []string) *exec.Cmd {
	cmd := proc.CommandContext(ctx, "cmd.exe")
	for _, arg := range extra {
		script += " " + cmdArgument(arg)
	}
	// cmd.exe does not use CommandLineToArgvW unquoting. Supply its raw /s /c
	// command line, just as Node's shell launcher does, preserving inner quotes.
	cmd.SysProcAttr.CmdLine = windows.EscapeArg(cmd.Path) + ` /d /s /c "` + script + `"`
	return cmd
}

// Quote one literal argument for cmd's shell layer plus the executable argv
// layer. The two-stage algorithm is also used by node-cross-spawn:
// https://github.com/moxystudio/node-cross-spawn/blob/master/lib/util/escape.js
func cmdArgument(s string) string {
	var quoted strings.Builder
	quoted.WriteByte('"')
	slashes := 0
	for _, r := range s {
		if r == '\\' {
			slashes++
			continue
		}
		if r == '"' {
			quoted.WriteString(strings.Repeat("\\", 2*slashes+1))
		} else {
			quoted.WriteString(strings.Repeat("\\", slashes))
		}
		quoted.WriteRune(r)
		slashes = 0
	}
	quoted.WriteString(strings.Repeat("\\", 2*slashes))
	quoted.WriteByte('"')
	var escaped strings.Builder
	for _, r := range quoted.String() {
		if strings.ContainsRune("()[]%!^\"`<>&|;, *?", r) {
			escaped.WriteByte('^')
		}
		escaped.WriteRune(r)
	}
	return escaped.String()
}
