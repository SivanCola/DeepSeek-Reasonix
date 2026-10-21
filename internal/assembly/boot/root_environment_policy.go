package boot

import (
	"context"
	"errors"

	"reasonix/internal/contract/config"
	"reasonix/internal/runtime/rootenv"
	"reasonix/internal/session/control"
	"reasonix/internal/state/sessionstore"
)

func (b *builder) rootEnvironmentPolicy() *control.RootEnvironmentPolicy {
	if b.rootEnvironment != nil {
		return &control.RootEnvironmentPolicy{Generation: b.ext.generation, Publish: b.publishRootEnvironment, Recheck: b.recheckRootEnvironment,
			Retry: b.retryRootEnvironment, Results: b.rootEnvironmentResults, Recover: b.recoverRootEnvironment}
	}
	if b.session.mutations == nil {
		return nil
	}
	return &control.RootEnvironmentPolicy{Generation: b.ext.generation, Block: func() error {
		if b.opts.FileOverlay != nil || b.opts.TerminalRunner != nil {
			return errors.New("client file or terminal services cannot move automatically")
		}
		if b.ext.mgr != nil && len(b.ext.mgr.Clients()) > 0 {
			return errors.New("extension processes already belong to this runtime")
		}
		if b.tools.host != nil && len(b.tools.host.Servers()) > 0 {
			return errors.New("configured MCP runtimes cannot move automatically")
		}
		if b.tools.taskTool != nil && b.tools.taskTool.Scheduler().ActiveWriters() > 0 {
			return errors.New("delegated writers are already active")
		}
		return nil
	}, Prepare: func(ctx context.Context, path string) (*rootenv.Environment, *config.Config, error) {
		cfg, err := b.cfg.CloneResolved()
		if err != nil {
			return nil, nil, err
		}
		store, err := rootenv.New(b.roots.DeliveryWorktreeDir(), b.root, b.repo, b.session.mutations)
		if err != nil {
			return nil, nil, err
		}
		store.WithVerificationChecks(b.prompt.projectChecks)
		if b.tools.taskTool != nil {
			store.WithPreparationCapacities(b.tools.taskTool.Scheduler().PhaseCapacity().Preparation)
		}
		denied := appendUniquePaths(RuntimeForbidReadRoots(cfg, b.root), b.opts.inheritedReadDenies...)
		environment, err := store.Prepare(ctx, sessionstore.CanonicalSessionPath(path), cfg.Agent.IsolationCopyPaths, denied)
		return environment, cfg, err
	}}
}
