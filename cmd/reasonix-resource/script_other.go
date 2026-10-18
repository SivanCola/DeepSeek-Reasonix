//go:build !windows

package main

import (
	"context"
	"os/exec"

	"reasonix/internal/proc"
)

func scriptCommand(ctx context.Context, script string, extra []string) *exec.Cmd {
	if len(extra) != 0 {
		script += ` "$@"`
	}
	return proc.CommandContext(ctx, "sh", append([]string{"-c", script, "reasonix-package"}, extra...)...)
}
