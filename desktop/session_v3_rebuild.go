package main

import (
	"context"

	"reasonix/internal/boot"
	"reasonix/internal/control"
	"reasonix/internal/sessionv3"
)

// sessionV3Binding is intentionally smaller than the public desktop control
// surface. It lets rebuild code preserve the exact host-owned Runtime without
// making legacy test controllers implement the final identity API.
type sessionV3Binding interface {
	SessionV3Binding() (*sessionv3.Service, *sessionv3.Runtime, bool)
}

func exclusiveV3Binding(ctrl control.SessionAPI) (*sessionv3.Service, *sessionv3.Runtime, bool) {
	bound, ok := ctrl.(sessionV3Binding)
	if !ok || bound == nil {
		return nil, nil, false
	}
	service, runtime, exclusive := bound.SessionV3Binding()
	return service, runtime, exclusive && service != nil && runtime != nil
}

// buildDesktopControllerReplacement uses boot.Rebuild for an already-bound
// final-format session. That path keeps the SessionRuntime and its writer while
// replacing only the Agent/configuration graph. Legacy controllers retain the
// old build-and-resume path until they cross the explicit migration boundary.
func buildDesktopControllerReplacement(ctx context.Context, old control.SessionAPI, opts boot.Options) (control.SessionAPI, bool, error) {
	concrete, ok := old.(*control.Controller)
	if !ok || concrete == nil {
		ctrl, err := boot.Build(ctx, opts)
		return ctrl, false, err
	}
	if opts.SessionService == nil {
		ctrl, err := boot.Build(ctx, opts)
		return ctrl, false, err
	}
	if opts.SessionTemp == nil {
		opts.SessionTemp = concrete.SessionTemp()
	}
	result, err := boot.Rebuild(ctx, concrete, opts)
	if err != nil {
		return nil, true, err
	}
	return result.Controller, true, nil
}

// retireReplacedController must not close a SessionRuntime still used by its
// replacement. ReleaseResources tears down only the retired Agent generation.
func retireReplacedController(old, replacement control.SessionAPI) {
	if old == nil || old == replacement {
		return
	}
	_, oldRuntime, oldExclusive := exclusiveV3Binding(old)
	_, newRuntime, newExclusive := exclusiveV3Binding(replacement)
	if oldExclusive && newExclusive && oldRuntime == newRuntime {
		if concrete, ok := replacement.(*control.Controller); ok {
			concrete.ActivateGoalDriverAfterRebuild()
		}
		if concrete, ok := old.(*control.Controller); ok {
			concrete.ReleaseResources()
			return
		}
	}
	old.Close()
}

func discardReplacementController(candidate, current control.SessionAPI) {
	if candidate == nil || candidate == current {
		return
	}
	_, candidateRuntime, candidateExclusive := exclusiveV3Binding(candidate)
	_, currentRuntime, currentExclusive := exclusiveV3Binding(current)
	if candidateExclusive && currentExclusive && candidateRuntime == currentRuntime {
		if concrete, ok := candidate.(*control.Controller); ok {
			concrete.ReleaseResources()
			return
		}
	}
	candidate.Close()
}
