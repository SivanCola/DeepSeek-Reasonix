package delegation

import (
	"context"
	"fmt"
	"reasonix/internal/runtime/agent"
)

func (t *TaskTool) dispatchProfile(ctx context.Context, spec ProfileExecSpec) (string, error, bool) {
	if t == nil {
		return "", fmt.Errorf("task tool is not configured"), true
	}
	if err := agent.CheckSharedTaskBudget(ctx); err != nil {
		return "", err, true
	}
	if spec.Sched.Isolation != "" {
		result, err := t.runIsolatedProfile(ctx, spec)
		return result, err, true
	}
	return "", nil, false
}

func (t *TaskTool) backgroundContext(dst, src context.Context, parent string) context.Context {
	dst = agent.WithParentSession(dst, parent)
	dst = agent.InheritTaskBudget(dst, src)
	return WithExecutionSessionPath(dst, t.executionSessionPath(src))
}
