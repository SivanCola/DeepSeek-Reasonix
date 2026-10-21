package control

import (
	"context"
	"crypto/rand"
	"errors"

	"reasonix/internal/contract/tool"
	"reasonix/internal/platform/worktree"
	"reasonix/internal/runtime/isolation"
	"reasonix/internal/state/mutation"
	"reasonix/internal/state/sessionstore"
)

type isolatedResultsState struct {
	owner    IsolatedResultsOwner
	access   func(context.Context, []string) error
	identity string
}

func newIsolatedResultsState(opts Options) isolatedResultsState {
	return isolatedResultsState{owner: opts.IsolatedResults, access: opts.IsolationRecoveryAccess, identity: rand.Text()}
}

type IsolatedResultsOwner interface {
	Catalog(context.Context) (isolation.Catalog, error)
	PreviewDiscard(context.Context, string) (isolation.DiscardPreview, error)
	DiscardResultExpected(context.Context, string, string) error
}

type isolationRecoveryOwner interface {
	PreviewRecovery(context.Context) (*worktree.RecoveryPreview, error)
	RecoverExpected(context.Context, string, isolation.SourceAuthorizer) error
}

func (c *Controller) previewIsolationRecovery(ctx context.Context) (*worktree.RecoveryPreview, error) {
	if owner, ok := c.isolatedResults.owner.(isolationRecoveryOwner); ok {
		return owner.PreviewRecovery(ctx)
	}
	return worktree.PreviewRecovery(c.WorkspaceRepo())
}

type IsolatedDiscardRequest struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

func (c *Controller) IsolationResults(ctx context.Context) (isolation.Catalog, error) {
	if c.isolatedResults.owner == nil {
		out, err := (*isolation.Store)(nil).Catalog(ctx)
		return c.withRootScene(ctx, out), err
	}
	out, err := c.isolatedResults.owner.Catalog(ctx)
	out = c.withRootScene(ctx, out)
	if err != nil || !out.RecoveryRequired {
		return out, err
	}
	preview, err := c.previewIsolationRecovery(ctx)
	if err != nil {
		out.RecoveryReason = err.Error()
		return out, nil
	}
	if preview != nil && c.mutations != nil && c.isolatedResults.access != nil {
		out.Recovery = &isolation.RecoveryTarget{Workspace: c.WorkspaceRoot(), Runtime: c.isolatedResults.identity, Revision: preview.Revision, Paths: preview.Paths}
	}
	return out, nil
}

func (c *Controller) PreviewIsolatedDiscard(ctx context.Context, id string) (isolation.DiscardPreview, error) {
	if c.isolatedResults.owner == nil {
		return isolation.DiscardPreview{}, tool.Refusal{Code: isolation.CodeUnavailable, Message: "isolated result management is unavailable"}
	}
	return c.isolatedResults.owner.PreviewDiscard(ctx, id)
}

func (c *Controller) DiscardIsolated(ctx context.Context, request IsolatedDiscardRequest) error {
	if c.isolatedResults.owner == nil {
		return tool.Refusal{Code: isolation.CodeUnavailable, Message: "isolated result management is unavailable"}
	}
	return c.isolatedResults.owner.DiscardResultExpected(ctx, request.ID, request.Revision)
}

func (c *Controller) RecoverIsolatedIntegration(ctx context.Context, target isolation.RecoveryRequest) error {
	if err := c.PrepareIsolatedRecovery(ctx, target); err != nil {
		return err
	}
	validate := c.MutationExecutionValidator(sessionstore.BranchID(c.SessionPath()))
	if err := validate(ctx); err != nil {
		return err
	}
	return c.recoverIsolatedIntegration(ctx, target, validate)
}

// PrepareIsolatedRecovery establishes a fresh pane's writer binding. A host
// that replaces controllers serializes this short phase with publication.
func (c *Controller) PrepareIsolatedRecovery(ctx context.Context, target isolation.RecoveryRequest) error {
	if c.mutations == nil || c.isolatedResults.access == nil {
		return tool.Refusal{Code: isolation.CodeUnavailable, Message: "integration recovery requires the new coordination strategy"}
	}
	if target.Runtime != c.isolatedResults.identity || target.Workspace != c.WorkspaceRoot() || target.Revision == "" {
		return tool.Refusal{Code: isolation.CodeStaleExecution, Message: "recovery preview belongs to another runtime or workspace; refresh before continuing"}
	}
	if err := c.CheckMutationExecution(ctx); err != nil {
		return err
	}
	c.EnsureSessionPath()
	return c.CheckMutationExecution(ctx)
}

func (c *Controller) recoverIsolatedIntegration(ctx context.Context, target isolation.RecoveryRequest, validate func(context.Context) error) error {
	repo := c.WorkspaceRepo()
	preview, err := c.previewIsolationRecovery(ctx)
	if err != nil {
		return err
	}
	if preview == nil || preview.Revision != target.Revision {
		return tool.Refusal{Code: isolation.CodeStaleExecution, Message: "integration journal changed after preview; refresh before recovering"}
	}
	paths := preview.Paths
	authorize := func(paths []string) error {
		if err := validate(ctx); err != nil {
			return err
		}
		return c.isolatedResults.access(ctx, paths)
	}
	if err := authorize(paths); err != nil {
		return err
	}
	activity, err := c.holdWorkspaceActivity(ctx)
	if err != nil {
		return err
	}
	defer activity.release()
	if owner, ok := c.isolatedResults.owner.(isolationRecoveryOwner); ok {
		return owner.RecoverExpected(ctx, target.Revision, func(_ context.Context, paths []string) error { return authorize(paths) })
	}
	lease, err := c.mutations.Acquire(ctx, mutation.Intent{Effect: mutation.NamedPaths,
		Paths: append(paths, repo.GitDir, repo.CommonDir), CallID: "isolation.user_recover", Recovery: true})
	if err != nil {
		return err
	}
	defer lease.End(mutation.Unknown)
	recoveryCtx := worktree.WithRecoveryAuthorization(lease.Context(ctx), func(paths []string) error {
		if err := authorize(paths); err != nil {
			return err
		}
		return lease.CheckPaths(paths)
	})
	err = worktree.RecoverIntegrationExpected(recoveryCtx, repo, target.Revision)
	if errors.Is(err, worktree.ErrRecoveryChanged) {
		return tool.Refusal{Code: isolation.CodeStaleExecution, Message: "integration journal changed after preview; files retained"}
	}
	return err
}
