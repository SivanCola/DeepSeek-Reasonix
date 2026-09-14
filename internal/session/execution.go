package session

import (
	"context"
)

// ExecutionControl is the session-scoped turn-loop bound to a Runtime.
// The loop is the execution authority; Runtime only forwards Cancel and
// caches the last NoteExecution from the bound generation.
type ExecutionControl interface {
	Snapshot() RuntimeSnapshot
	Cancel() bool
}

type executionBinding struct {
	generation uint64
	control    ExecutionControl
}

// BindExecution installs a generation-scoped turn-loop and returns the
// generation. A later bind always wins; UnbindExecution of an older
// generation cannot clear the replacement.
func (r *Runtime) BindExecution(control ExecutionControl) uint64 {
	if r == nil || control == nil {
		return 0
	}
	gen := r.bindGen.Add(1)
	next := &executionBinding{generation: gen, control: control}
	for {
		cur := r.execution.Load()
		if r.execution.CompareAndSwap(cur, next) {
			r.lastGen.Store(gen)
			r.revision.Add(1)
			return gen
		}
	}
}

// UnbindExecution releases only the exact generation. A superseded controller
// cannot clear the replacement's control binding.
func (r *Runtime) UnbindExecution(generation uint64) {
	if r == nil || generation == 0 {
		return
	}
	for {
		cur := r.execution.Load()
		if cur == nil || cur.generation != generation {
			return
		}
		if r.execution.CompareAndSwap(cur, nil) {
			r.revision.Add(1)
			return
		}
	}
}

// NoteExecution records a phase transition from the bound generation. After
// that generation unbinds, it may still settle to idle or recovery_required so
// a closing controller can release the writer; a newer generation ignores it.
func (r *Runtime) NoteExecution(generation uint64, phase RuntimePhase, activity string) {
	if r == nil || generation == 0 {
		return
	}
	cur := r.execution.Load()
	if cur != nil && cur.generation != generation {
		return
	}
	if cur == nil && r.lastGen.Load() != generation {
		return
	}
	r.mu.Lock()
	if r.phase == RuntimeClosed {
		r.mu.Unlock()
		return
	}
	if r.phase == RuntimeRecoveryRequired && phase != RuntimeRecoveryRequired && phase != RuntimeClosed {
		r.mu.Unlock()
		return
	}
	if r.phase == phase && r.activity == activity {
		r.mu.Unlock()
		return
	}
	r.phase = phase
	r.activity = activity
	r.revision.Add(1)
	if phase == RuntimeCancelling || phase == RuntimeRecoveryRequired {
		r.canceling.Store(true)
	} else {
		r.canceling.Store(false)
	}
	owner := r.owner
	r.mu.Unlock()
	if phase == RuntimeIdle && owner != nil {
		_ = owner.closeIfUnbound(context.Background(), r)
	}
}

func (r *Runtime) loadExecution() *executionBinding {
	if r == nil {
		return nil
	}
	return r.execution.Load()
}

func (p RuntimePhase) busy() bool {
	switch p {
	case RuntimeRunning, RuntimeCancelling, RuntimeFinalizing, RuntimeRecoveryRequired:
		return true
	default:
		return false
	}
}

func (r *Runtime) executionBusy() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	phase := r.phase
	r.mu.Unlock()
	return phase.busy()
}
