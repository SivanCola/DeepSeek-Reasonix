package builtin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"reasonix/internal/persistentshell"
	"reasonix/internal/sandbox"
	"reasonix/internal/shellrun"
	"reasonix/internal/tool"
)

func persistEnv(env []string) []string {
	return applyEnvOverrides(env, []string{
		"TERM=dumb",
		"NO_COLOR=1",
		"PAGER=cat",
		"GIT_PAGER=cat",
		"BASH_SILENCE_DEPRECATION_WARNING=1",
	})
}

func (b bash) persistentManager(ctx context.Context) *persistentshell.Manager {
	if m := persistentshell.FromContext(ctx); m != nil {
		return m
	}
	return b.persistent
}

func (b bash) shouldUsePersistent(ctx context.Context, p bashParams) bool {
	if p.RunInBackground || p.PreserveBackgroundProcesses {
		return false
	}
	if len(p.AdditionalWriteDirs) > 0 || strings.TrimSpace(p.SandboxPermissions) != "" {
		return false
	}
	m := b.persistentManager(ctx)
	return m != nil && !m.Sealed()
}

func rejectPowerShellChaining(ex *tool.ShellExecution, start time.Time, sh sandbox.Shell, command string) (tool.DetailedResult, error, bool) {
	if sh.SupportsChaining() || (!hasUnquotedSeq(command, "&&") && !hasUnquotedSeq(command, "||")) {
		return tool.DetailedResult{}, nil, false
	}
	ex.State = tool.ShellStateNotRun
	ex.FailurePhase = tool.ShellPhasePreflight
	ex.MutationRisk = tool.ShellMutationNotStarted
	ex.DurationMs = time.Since(start).Milliseconds()
	return tool.DetailedResult{Execution: ex}, fmt.Errorf("this shell is Windows PowerShell, which does not parse '&&' or '||'. " +
		"Sequence with ';' (both run regardless of the first's result), use 'if ($?) { ... }' for " +
		"conditional chaining, or issue the commands as separate calls"), true
}

func (b bash) tryPersistent(ctx context.Context, p bashParams, sh sandbox.Shell, cmdEnv []string, wrapped bool, start time.Time, ex *tool.ShellExecution) (tool.DetailedResult, error, bool) {
	out, runEx, err, used := b.runPersistent(ctx, p, sh, cmdEnv)
	if !used {
		return tool.DetailedResult{}, nil, false
	}
	mergeRunInto(ex, runEx)
	ex.DurationMs = time.Since(start).Milliseconds()
	return tool.DetailedResult{
		Output:    b.appendWriteHints(ctx, out, err, p, wrapped),
		Execution: ex,
	}, err, true
}

func (b bash) runPersistent(ctx context.Context, p bashParams, sh sandbox.Shell, cmdEnv []string) (string, *tool.ShellExecution, error, bool) {
	if !b.shouldUsePersistent(ctx, p) {
		return "", nil, nil, false
	}
	m := b.persistentManager(ctx)
	spec := b.specForCall(ctx)
	argv, wrapped := sandbox.CommandArgs(spec, persistentshell.InteractiveArgv(sh))
	if spec.Enforce() && !wrapped {
		return "", nil, fmt.Errorf("%s", sandbox.UnavailableMessage()), true
	}
	var progress func(string)
	if emit, ok := tool.ProgressFrom(ctx); ok {
		progress = emit
	}
	res := m.Run(ctx, persistentshell.Request{
		Argv:     argv,
		Dir:      b.workDir,
		Env:      cmdEnv,
		Command:  p.Command,
		Timeout:  b.foregroundTimeout(),
		Shell:    sh,
		Progress: progress,
	})
	if !res.Started && res.Err != nil {
		return "", nil, nil, false
	}
	ex := shellrun.DescriptorFromShell(sh)
	ex.State = res.State
	ex.FailurePhase = res.FailurePhase
	code := res.ExitCode
	ex.ExitCode = &code
	if res.State != tool.ShellStateCompleted && res.Output != "" {
		ex.OutputTail = res.Output
		if len(ex.OutputTail) > tool.OutputTailMaxBytes {
			ex.OutputTail = ex.OutputTail[len(ex.OutputTail)-tool.OutputTailMaxBytes:]
		}
	}
	switch res.State {
	case tool.ShellStateCompleted:
		ex.MutationRisk = tool.ShellMutationMayHaveCompleted
	case tool.ShellStateNotRun:
		ex.MutationRisk = tool.ShellMutationNotStarted
	case tool.ShellStateFailed:
		if res.FailurePhase == tool.ShellPhaseLaunch || res.FailurePhase == tool.ShellPhasePreflight {
			ex.MutationRisk = tool.ShellMutationNotStarted
		} else {
			ex.MutationRisk = tool.ShellMutationMayBePartial
		}
	case tool.ShellStateTimedOut, tool.ShellStateCancelled:
		ex.MutationRisk = tool.ShellMutationMayBePartial
	default:
		ex.MutationRisk = tool.ShellMutationUnknown
	}
	return res.Output, ex, res.Err, true
}
