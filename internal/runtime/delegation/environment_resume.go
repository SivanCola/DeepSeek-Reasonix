package delegation

import (
	"context"
	"strings"

	"reasonix/internal/contract/tool"
)

func (t *TaskTool) resumeEnvironment(ctx context.Context, spec ProfileExecSpec) (string, error) {
	ref := firstNonEmpty(spec.Context.ContinueFrom, spec.Context.ForkFrom)
	if ref == "" || t.transcripts == nil {
		return "", nil
	}
	if spec.Context.parentSession(ctx) == "" {
		return "", nil
	}
	meta, err := t.transcripts.LoadMeta(ref)
	if err != nil {
		return "", err
	}
	if meta.EnvironmentID == "" {
		return "", nil
	}
	parent := strings.TrimSpace(spec.Context.parentSession(ctx))
	if parent == "" {
		return "", tool.Refusal{Code: "isolation.unsupported_arg", Message: "isolated continuation requires its owning session"}
	}
	if err := validateContinueOwner(meta, SubagentSpec{ParentSession: parent}); err != nil {
		return "", err
	}
	if !transcriptInWorkspace(meta, firstNonEmpty(t.environmentSource, t.workspaceRoot)) {
		return "", tool.Refusal{Code: "isolation.unsupported_arg", Message: "isolated transcript belongs to a different workspace"}
	}
	if spec.Context.ForkFrom != "" || spec.Sched.Environment == "shared" {
		return "", tool.Refusal{Code: "isolation.unsupported_arg", Message: "continue this isolated task in its retained environment; a shared continuation or environment fork is unavailable"}
	}
	return meta.EnvironmentID, nil
}
