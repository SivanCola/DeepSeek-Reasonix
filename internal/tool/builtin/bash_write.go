package builtin

import (
	"context"
	"fmt"
	"strings"

	"reasonix/internal/sandbox"
)

func validateBashWriteDirs(p bashParams) error {
	if len(p.AdditionalWriteDirs) == 0 {
		return nil
	}
	if strings.TrimSpace(p.Justification) == "" {
		return fmt.Errorf("justification is required when additional_write_dirs is set")
	}
	for _, dir := range p.AdditionalWriteDirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return fmt.Errorf("additional_write_dirs entries must be non-empty directories")
		}
		if strings.ContainsAny(dir, "*?[") {
			return fmt.Errorf("additional_write_dirs %q must be a concrete directory, not a glob", dir)
		}
	}
	return nil
}

func (b bash) specForCall(ctx context.Context) sandbox.Spec {
	spec := b.sb
	if b.rootSet != nil {
		spec.WriteRoots = b.rootSet.Effective(ctx)
	} else if extra := sandbox.PerCallWriteRoots(ctx); len(extra) > 0 {
		spec.WriteRoots = sandbox.CollapseWriteRoots(append(append([]string{}, spec.WriteRoots...), extra...))
	}
	if spec.ProtectedWriteRoots == nil && b.guard.stateRoot != "" {
		spec.ProtectedWriteRoots = sandbox.ProtectedWriteRoots(b.guard.stateRoot)
	}
	return spec
}

func bashWriteDeniedHint() string {
	return "The OS sandbox blocked a write outside the approved writable roots. Retry the same command with structured additional_write_dirs naming the exact directories (no globs), plus a justification. Example: {\"command\":\"mkdir -p ~/.local/bin && cp tool ~/.local/bin/tool\",\"additional_write_dirs\":[\"~/.local\"],\"justification\":\"install the user-requested local command\"}. Do not retry unconfined and do not omit the directories."
}

func looksLikeSandboxWriteDenial(out string, err error) bool {
	msg := strings.ToLower(out)
	if err != nil {
		msg += "\n" + strings.ToLower(err.Error())
	}
	for _, needle := range []string{
		"operation not permitted",
		"read-only file system",
		"erofs",
		"permission denied",
		"sandbox",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func appendSandboxWriteHint(out string, err error, p bashParams, spec sandbox.Spec) string {
	if !spec.Enforce() || len(p.AdditionalWriteDirs) > 0 || !looksLikeSandboxWriteDenial(out, err) {
		return out
	}
	return appendSessionDataHint(out, bashWriteDeniedHint())
}
