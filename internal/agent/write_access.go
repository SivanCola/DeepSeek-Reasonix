package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"reasonix/internal/sandbox"
	"reasonix/internal/tool"
	"reasonix/internal/tool/builtin"
)

const (
	headlessWriteAccessHint = "this directory is outside the writable roots. Restart with --add-dir /abs/path, add it to [sandbox].allow_write in reasonix.toml, or use an interactive session to approve the directory."
	subagentWriteAccessHint = "this sub-agent cannot expand write access. Ask the parent agent to request the directories (bash additional_write_dirs, or write the file after the parent is granted that directory)."
)

// SubagentWriteAccessMessage is the structured failure a child agent returns
// when it needs a directory the parent has not granted.
func SubagentWriteAccessMessage(display []string) string {
	if len(display) == 0 {
		return subagentWriteAccessHint
	}
	return subagentWriteAccessHint + " Needed directories: " + strings.Join(display, ", ")
}

// WriteAccessCheck is the host-local write-directory preflight for one tool call.
type WriteAccessCheck struct {
	Tool        string
	Subject     string
	Args        json.RawMessage
	ReadOnly    bool
	Declaration tool.WriteAccessDeclaration
	Expandable  bool
}

// WriteAccessDecision is the result of a write-directory preflight.
type WriteAccessDecision struct {
	Allow            bool
	Reason           string
	PerCallRoots     []string
	SkipOrdinaryGate bool
}

// WriteAccessGate authorizes extra writable directories before a tool runs.
type WriteAccessGate interface {
	CheckWriteAccess(ctx context.Context, req WriteAccessCheck) (WriteAccessDecision, error)
}

func (a *Agent) applyWriteAccess(ctx context.Context, plan *toolCallPlan) (toolOutcome, bool) {
	if a == nil || plan == nil || plan.readOnly {
		return toolOutcome{}, false
	}
	decl, ok := plan.execTool.(tool.WriteAccessDeclarer)
	if !ok {
		return toolOutcome{}, false
	}
	declaration, err := decl.DeclareWriteAccess(plan.permArgs)
	if err != nil {
		return toolOutcome{
			output:  fmt.Sprintf("error: %v", err),
			errMsg:  firstLine(err.Error()),
			blocked: true,
		}, true
	}
	if a.svc.writeAccess == nil {
		if a.svc.writeRoots == nil || len(declaration.Directories) == 0 {
			return toolOutcome{}, false
		}
		abs, display, _, nerr := sandbox.NormalizeWriteDirs(declaration.Directories, a.workspaceRoot(), a.homeDir(), a.stateRoot())
		if nerr != nil {
			return writeAccessBlocked(nerr.Error()), true
		}
		if left := a.svc.writeRoots.Missing(abs); len(left) > 0 {
			if !a.svc.writeAccessExpandable {
				return writeAccessBlocked(SubagentWriteAccessMessage(displayList(display))), true
			}
			return writeAccessBlocked(headlessWriteAccessHint + " Needed: " + strings.Join(displayList(display), ", ")), true
		}
		return toolOutcome{}, false
	}
	dec, err := a.svc.writeAccess.CheckWriteAccess(ctx, WriteAccessCheck{
		Tool:        plan.permName,
		Subject:     permissionSubject(plan),
		Args:        plan.permArgs,
		ReadOnly:    plan.readOnly,
		Declaration: declaration,
		Expandable:  a.svc.writeAccessExpandable,
	})
	if err != nil {
		return toolOutcome{
			output:  fmt.Sprintf("blocked: %v", err),
			blocked: true,
			errMsg:  firstLine(err.Error()),
		}, true
	}
	if !dec.Allow {
		msg := strings.TrimSpace(dec.Reason)
		if msg == "" {
			msg = "write access was denied"
		}
		if !strings.HasPrefix(msg, "blocked:") {
			msg = "blocked: " + msg
		}
		return toolOutcome{output: msg, blocked: true, errMsg: firstLine(msg)}, true
	}
	plan.perCallWriteRoots = dec.PerCallRoots
	plan.skipOrdinaryGate = dec.SkipOrdinaryGate
	return toolOutcome{}, false
}

func writeAccessBlocked(reason string) toolOutcome {
	msg := strings.TrimSpace(reason)
	if !strings.HasPrefix(msg, "blocked:") {
		msg = "blocked: " + msg
	}
	return toolOutcome{output: msg, blocked: true, errMsg: firstLine(msg)}
}

func permissionSubject(plan *toolCallPlan) string {
	if plan == nil {
		return ""
	}
	if plan.evidenceName == "bash" {
		return strings.TrimSpace(bashCommandFromArgs(plan.permArgs))
	}
	return strings.TrimSpace(string(plan.permArgs))
}

func displayList(dirs []string) []string {
	if dirs == nil {
		return []string{}
	}
	return dirs
}

func (a *Agent) workspaceRoot() string {
	if a == nil {
		return ""
	}
	return strings.TrimSpace(a.svc.workspaceRoot)
}

func (a *Agent) homeDir() string {
	if a != nil && a.svc.homeDir != "" {
		return a.svc.homeDir
	}
	home, _ := os.UserHomeDir()
	return home
}

func (a *Agent) stateRoot() string {
	if a == nil {
		return ""
	}
	return strings.TrimSpace(a.svc.stateRoot)
}

func (a *Agent) stampWriteRoots(ctx context.Context, plan *toolCallPlan) context.Context {
	if len(plan.perCallWriteRoots) > 0 {
		ctx = sandbox.WithPerCallWriteRoots(ctx, plan.perCallWriteRoots)
	}
	return ctx
}

// BindChildWriteRoots rebinds writer/bash tools onto a snapshot of the parent
// set (or the write_paths intersection). Later parent grants cannot expand the
// child. A whole-workspace or empty claim inherits the current snapshot.
func BindChildWriteRoots(reg *tool.Registry, parent *sandbox.WritableRootSet, claims WritePathSet) (*tool.Registry, *sandbox.WritableRootSet) {
	if parent == nil || reg == nil {
		return reg, parent
	}
	cap := claims.Roots()
	if claims.Empty() || claims.WholeWorkspace {
		cap = nil
	}
	childSet := parent.CloneRestricted(cap)
	for _, name := range reg.Names() {
		tl, ok := reg.Get(name)
		if !ok {
			continue
		}
		reg.Add(bindToolWriteRootSet(tl, childSet))
	}
	return reg, childSet
}

func bindToolWriteRootSet(tl tool.Tool, set *sandbox.WritableRootSet) tool.Tool {
	switch t := tl.(type) {
	case foregroundOnlyBash:
		t.inner = builtin.BindWriteRootSet(t.inner, set)
		return t
	case readOnlyBash:
		t.inner = builtin.BindWriteRootSet(t.inner, set)
		return t
	case pathBoundWriter:
		t.inner = builtin.BindWriteRootSet(t.inner, set)
		return t
	default:
		return builtin.BindWriteRootSet(tl, set)
	}
}
