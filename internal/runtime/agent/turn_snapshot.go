package agent

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"reasonix/internal/state/observation"
	"reasonix/internal/state/trustedstate"
)

// turnSnapshot is the workspace as the turn found it, taken while the first
// model request is in flight and waited on before any tool runs.
type turnSnapshot struct {
	done   chan struct{}
	cancel context.CancelFunc
	snap   observation.Snapshot
}

// snapshotWait bounds the snapshot at both ends of a turn; past it the snapshot
// records itself as cancelled rather than holding the turn open.
const snapshotWait = 10 * time.Second

// snapshotCancelGrace is how long a cancelled start-of-turn snapshot is given to
// stop before the first tool runs without it.
const snapshotCancelGrace = 2 * time.Second

// maxRecordsBack bounds how far back previousSnapshot looks for a bundle.
const maxRecordsBack = 8

// changedPathLimit bounds how many changed paths a bundle lists; the count is
// always exact.
const changedPathLimit = 200

func (a *Agent) startTurnSnapshot(ctx context.Context) {
	seal := a.svc.evidenceSeal
	if seal == nil || seal.Observer == nil || seal.Root == "" {
		return
	}
	snapCtx, cancel := context.WithCancel(ctx)
	ts := &turnSnapshot{done: make(chan struct{}), cancel: cancel}
	a.turn.snapshot = ts
	go func() {
		defer cancel()
		ts.snap = seal.take(snapCtx, seal.Root)
		close(ts.done)
	}()
}

// await holds the first tool until the turn-start snapshot exists, so nothing
// the turn does can land in what it is compared against, but never past limit:
// a workspace too big to observe in that time is observed incompletely, which
// the bundle records as a gap, rather than making every command wait for a walk.
func (ts *turnSnapshot) await(ctx context.Context, limit time.Duration) (observation.Snapshot, bool) {
	if ts == nil {
		return observation.Snapshot{}, false
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-ts.done:
		return ts.snap, true
	case <-ctx.Done():
		return observation.Snapshot{}, false
	case <-timer.C:
	}
	ts.cancel()
	grace := time.NewTimer(snapshotCancelGrace)
	defer grace.Stop()
	select {
	case <-ts.done:
		return ts.snap, true
	case <-ctx.Done():
	case <-grace.C:
	}
	return observation.Snapshot{}, false
}

// snapshotDelta is how two snapshots differ, or why that is not established.
type snapshotDelta struct {
	Compared bool     `json:"compared"`
	Reason   string   `json:"reason,omitempty"`
	Changed  int      `json:"changed,omitempty"`
	Paths    []string `json:"paths,omitempty"`
}

// Why a delta was not compared.
const (
	deltaNoPrevious    = "no_previous"
	deltaIncomparable  = "incomparable"
	deltaMissingBefore = "missing_before"
)

func compareSnapshots(o *observation.Observer, a, b observation.Snapshot) snapshotDelta {
	paths, count, err := o.Changed(a, b, changedPathLimit)
	switch {
	case errors.Is(err, observation.ErrIncomparable):
		return snapshotDelta{Reason: deltaIncomparable}
	case err != nil:
		return snapshotDelta{Reason: trustedstate.FailureCode(err)}
	}
	return snapshotDelta{Compared: true, Changed: count, Paths: paths}
}

// previousSnapshot reads the snapshot the stream's newest bundle ended with,
// stepping back over contract revisions sealed after it.
func previousSnapshot(seal *EvidenceSeal) (observation.Snapshot, bool) {
	head, err := seal.Store.Head(seal.Stream)
	if err != nil {
		return observation.Snapshot{}, false
	}
	rec, err := seal.Store.Record(head.Record)
	for step := 0; err == nil && !isShadowBundle(rec.Kind) && rec.Parent != "" && step < maxRecordsBack; step++ {
		rec, err = seal.Store.Record(rec.Parent)
	}
	if err != nil || !isShadowBundle(rec.Kind) {
		return observation.Snapshot{}, false
	}
	payload, err := seal.Store.Object(rec.Payload)
	if err != nil {
		return observation.Snapshot{}, false
	}
	var prior struct {
		After *observation.Snapshot `json:"snapshot_after"`
	}
	if json.Unmarshal(payload, &prior) != nil || prior.After == nil {
		return observation.Snapshot{}, false
	}
	return *prior.After, true
}

// workspaceObservation is what the bundle records about the workspace: the
// snapshots around the turn, what changed while nobody was observing, and
// what changed during the turn itself.
type workspaceObservation struct {
	Before     *observation.Snapshot `json:"snapshot_before,omitempty"`
	After      *observation.Snapshot `json:"snapshot_after,omitempty"`
	Unobserved snapshotDelta         `json:"unobserved"`
	WithinTurn snapshotDelta         `json:"within_turn"`
}

func (a *Agent) observeWorkspace(ctx context.Context, seal *EvidenceSeal) workspaceObservation {
	if seal.Observer == nil || seal.Root == "" {
		return workspaceObservation{}
	}
	waitCtx, cancel := context.WithTimeout(ctx, snapshotWait)
	defer cancel()
	result := make(chan workspaceObservation, 1)
	before := a.turn.snapshot
	// Filesystem calls may outlive cancellation. The worker captures no mutable
	// turn state, so a late observation cannot alter a replacement turn.
	go func() { result <- observeSnapshots(waitCtx, seal, before) }()
	select {
	case obs := <-result:
		return obs
	case <-waitCtx.Done():
		return workspaceObservation{
			Unobserved: snapshotDelta{Reason: observation.IncompleteCancelled},
			WithinTurn: snapshotDelta{Reason: observation.IncompleteCancelled},
		}
	}
}

func observeSnapshots(ctx context.Context, seal *EvidenceSeal, start *turnSnapshot) workspaceObservation {
	var obs workspaceObservation
	before, haveBefore := start.await(ctx, snapshotWait)
	after := seal.take(ctx, seal.Root)
	obs.After = &after
	if !haveBefore {
		obs.Unobserved = snapshotDelta{Reason: deltaMissingBefore}
		obs.WithinTurn = snapshotDelta{Reason: deltaMissingBefore}
		return obs
	}
	obs.Before = &before
	obs.WithinTurn = compareSnapshots(seal.Observer, before, after)
	if prev, ok := previousSnapshot(seal); ok {
		obs.Unobserved = compareSnapshots(seal.Observer, prev, before)
	} else {
		obs.Unobserved = snapshotDelta{Reason: deltaNoPrevious}
	}
	return obs
}
