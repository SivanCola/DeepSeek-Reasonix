package delegation

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"reasonix/internal/contract/tool"
	"reasonix/internal/runtime/agent"
	"reasonix/internal/runtime/isolation"
	"reasonix/internal/runtime/writeclaim"
	"reasonix/internal/tools/jobs"
)

// SetIsolation uses the ordinary delegation runner inside a separately rooted kernel.
func (t *TaskTool) SetIsolation(store *isolation.Store, root string, run func(context.Context, isolation.Run, ProfileExecSpec) (isolation.Outcome, error)) {
	t.isolated = &taskIsolation{store: store, root: root, profile: run}
}

// InheritDelegationOwner shares accounting and transcripts, never the source workspace's tools.
func (t *TaskTool) InheritDelegationOwner(parent *TaskTool) {
	t.transcripts, t.transcriptRoot, t.scheduler = parent.transcripts, firstNonEmpty(parent.transcriptRoot, parent.workspaceRoot), parent.scheduler
	t.maxSubagentDepth = parent.maxSubagentDepth
}

func (t *TaskTool) runIsolatedProfile(ctx context.Context, spec ProfileExecSpec) (string, error) {
	if agent.SubagentDepth(ctx) > 0 {
		return "", fmt.Errorf("worktree isolation must be requested by the owning root conversation")
	}
	if spec.Sched.Isolation != isolationMode {
		return "", tool.Refusal{Code: codeIsolationUnsupported, Message: `isolation takes only "worktree"`}
	}
	if t.isolated == nil || t.isolated.profile == nil {
		return "", tool.Refusal{Code: isolation.CodeUnavailable, Message: "worktree isolation is not enabled in this session ([agent] worktree_isolation)"}
	}
	if spec.Context.ContinueFrom != "" || spec.Context.ForkFrom != "" {
		return "", tool.Refusal{Code: codeIsolationUnsupported, Message: "continue_from and fork_from cannot resume in a fresh worktree; start a new task with selected context"}
	}
	if err := t.settleSpecPrompts(ctx, &spec); err != nil {
		return "", err
	}
	spec.Worker.Model, spec.Worker.Effort = t.childRuntimeRef(ctx, spec.Worker.Model, spec.Worker.Effort)
	spec.Sched.MaxSteps = t.childMaxStepsForContext(ctx, spec.Sched.MaxSteps)
	depth, err := t.nextSubagentDepth(ctx)
	if err != nil {
		return "", err
	}
	reg, err := t.childRegistry(&spec, depth, writeclaim.NewWriteGrant(spec.Grant.WritePaths), spec.Worker.Model)
	if err != nil {
		return "", err
	}
	ceiling, err := IntersectToolLists(t.parentReg, spec.Grant.ProfileTools, spec.Grant.CallTools)
	if err != nil {
		return "", err
	}
	if len(ceiling) == 0 {
		ceiling = reg.Names()
	}
	spec.Grant.CallTools, spec.Grant.ProfileTools = ceiling, nil
	spec.Sched.Isolation = ""
	background := spec.Sched.RunInBackground
	spec.Sched.RunInBackground = false
	execute := func(runCtx context.Context) (string, error) {
		if err := agent.CheckSharedTaskBudget(runCtx); err != nil {
			return "", err
		}
		return t.isolated.store.Execute(runCtx, func(childCtx context.Context, run isolation.Run) (isolation.Outcome, error) {
			childSpec := spec
			claim, err := relocateClaim(spec.Grant.WritePaths, t.workspaceRoot, run.WorkspaceRoot)
			if err != nil {
				return isolation.Outcome{}, err
			}
			childSpec.Grant.WritePaths = claim
			return t.isolated.profile(childCtx, run, childSpec)
		}, t.isolated.root, spec.Task.Objective, spec.Worker.Model)
	}
	if !background {
		return execute(ctx)
	}
	manager, ok := jobs.FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("background execution is unavailable")
	}
	job := manager.StartForSession(jobs.SessionFromContext(ctx), "task", firstNonEmpty(spec.Task.Description, spec.Worker.Name), func(jobCtx context.Context, _ io.Writer) (string, error) {
		jobCtx = agent.InheritTaskBudget(jobCtx, ctx)
		jobCtx = WithExecutionSessionPath(jobCtx, t.executionSessionPath(ctx))
		jobCtx = agent.WithRawUserInput(jobCtx, agent.RawUserInput(ctx, spec.Task.Objective))
		jobCtx = agent.WithChildMessageReceiver(agent.WithParentSession(jobCtx, agent.ParentSession(ctx)), agent.ChildMessageReceiverFrom(ctx))
		id, sink, _, _ := agent.CallContext(ctx)
		jobCtx = agent.WithCallContext(jobCtx, id, sink, nil, false)
		return execute(jobCtx)
	})
	return fmt.Sprintf("Isolated task started in background as %s; use wait to collect its retained result.", job.ID), nil
}

func relocateClaim(claim writeclaim.WritePathSet, source, target string) (writeclaim.WritePathSet, error) {
	if claim.WholeWorkspace {
		claim.WorkspaceRoot = target
		return claim, nil
	}
	var paths []string
	for _, p := range claim.Paths {
		if !writeclaim.PathWithin(source, p) {
			return writeclaim.WritePathSet{}, fmt.Errorf("isolated write claim escapes source workspace")
		}
		rel, err := filepath.Rel(source, p)
		if err != nil {
			return writeclaim.WritePathSet{}, err
		}
		paths = append(paths, rel)
	}
	return writeclaim.NormalizeWritePaths(target, paths)
}
