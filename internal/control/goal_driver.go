package control

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"reasonix/internal/event"
	goaldomain "reasonix/internal/goal"
	"reasonix/internal/sessionv3"
	"reasonix/internal/tool"
)

// waitForGoalTerminal is the CLI/ACP/bot observer. It owns no Activity and
// therefore cannot block the driver; one model TurnDone is not completion of
// an armed long-running goal.
func (c *Controller) waitForGoalTerminal(ctx context.Context) error {
	if c == nil || !c.exclusiveV3Enabled() {
		return nil
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		view, err := c.goalLifecycleView()
		if err != nil {
			return err
		}
		if view == nil || view.Phase != goaldomain.PhaseActive || view.Activation != goaldomain.ActivationArmed {
			return nil
		}
		select {
		case <-ctx.Done():
			c.Cancel()
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// goalRoundReservation fences one proposed automatic turn to the exact idle
// runtime and goal version observed before the required durability checkpoint.
// It is process-local and is never restored or inherited by a fork.
type goalRoundReservation struct {
	SessionID            string
	RuntimeEpoch         string
	IdleActivityRevision uint64
	Goal                 goaldomain.Ref
	Round                uint64

	mu        sync.Mutex
	admitted  *goaldomain.View
	runErr    error
	cancelled bool
}

func (r *goalRoundReservation) setAdmitted(view goaldomain.View) {
	r.mu.Lock()
	copy := view
	r.admitted = &copy
	r.mu.Unlock()
}

func (r *goalRoundReservation) admittedView() (*goaldomain.View, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.admitted == nil {
		return nil, false
	}
	copy := *r.admitted
	return &copy, true
}

func (r *goalRoundReservation) setResult(err error, cancelled bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.runErr, r.cancelled = err, cancelled
	r.mu.Unlock()
}

func (r *goalRoundReservation) result() (error, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runErr, r.cancelled
}

// kickGoalDriver publishes one level-triggered scheduling check. Duplicate
// idle notifications collapse to one worker; a successful worker admits at
// most one ordinary top-level turn and the next TurnDone supplies a fresh kick.
func (c *Controller) kickGoalDriver() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.goalDriverMu.Lock()
	if c.goalDriverPending {
		c.goalDriverMu.Unlock()
		c.mu.Unlock()
		return
	}
	c.goalDriverPending = true
	c.goalDriverWG.Add(1)
	c.goalDriverMu.Unlock()
	c.mu.Unlock()
	go func() {
		defer c.goalDriverWG.Done()
		started := c.driveOneGoalRound()
		c.goalDriverMu.Lock()
		c.goalDriverPending = false
		finishedBeforeRelease := started && c.goalDriverActive == nil
		c.goalDriverMu.Unlock()
		if finishedBeforeRelease {
			c.kickGoalDriver()
		}
	}()
}

func (c *Controller) driveOneGoalRound() bool {
	view, runtime, snapshot, ok := c.goalRoundEligibility()
	if !ok {
		return false
	}
	if used, limit, exhausted := c.goalResourceBudget(); exhausted {
		_, _ = c.applyHostGoalMutation(context.Background(), "resource-budget", func(machine *goaldomain.Machine) (*goaldomain.View, error) {
			blocked, err := machine.Block(view.Ref(), goaldomain.BlockReason{Code: "resource-budget", Message: fmt.Sprintf("the configured goal token budget was reached (%d/%d tokens)", used, limit)}, true, 0)
			return &blocked, err
		})
		return false
	}
	if view.MaxGoalRounds != nil && view.RoundsStarted >= *view.MaxGoalRounds {
		_, _ = c.applyHostGoalMutation(context.Background(), "round-limit", func(machine *goaldomain.Machine) (*goaldomain.View, error) {
			blocked, err := machine.Block(view.Ref(), goaldomain.BlockReason{Code: "round-limit", Message: "the configured automatic goal round limit was reached"}, true, 0)
			return &blocked, err
		})
		return false
	}
	reservation := &goalRoundReservation{
		SessionID: snapshot.Ref.SessionID, RuntimeEpoch: snapshot.Epoch,
		IdleActivityRevision: snapshot.ActivityRevision,
		Goal:                 view.Ref(), Round: view.RoundsStarted + 1,
	}
	// This is a semantic checkpoint: no downstream model call starts unless all
	// already accepted events are durable.
	if _, err := runtime.Session().Flush(context.Background()); err != nil {
		c.disarmGoalLifecycle("persistence-error")
		c.noticeDetail("Goal automatic continuation stopped because session persistence failed.", err.Error())
		return false
	}
	current, currentRuntime, currentSnapshot, ok := c.goalRoundEligibility()
	if !ok || currentRuntime != runtime || currentSnapshot.Ref.SessionID != reservation.SessionID ||
		currentSnapshot.Epoch != reservation.RuntimeEpoch || currentSnapshot.ActivityRevision != reservation.IdleActivityRevision ||
		current.ID != reservation.Goal.ID || current.Revision != reservation.Goal.Revision || current.RoundsStarted+1 != reservation.Round {
		return false
	}
	prompt, err := goaldomain.ContinuationPrompt(*current)
	if err != nil {
		return false
	}
	c.goalDriverMu.Lock()
	c.goalDriverActive = reservation
	c.goalDriverMu.Unlock()
	result := c.runGuardedGoalRound(reservation, func(ctx context.Context) error {
		return newTurnOrchestrator(c).runOrchestratedTurn(ctx, orchestratedTurn{
			input: prompt, raw: prompt, synthetic: true, goalRound: reservation,
		})
	})
	if result != turnStarted {
		c.goalDriverMu.Lock()
		if c.goalDriverActive == reservation {
			c.goalDriverActive = nil
		}
		c.goalDriverMu.Unlock()
	}
	return result == turnStarted
}

func (c *Controller) recordGoalLifecycleUsage(e event.Event) {
	if c == nil || !c.exclusiveV3Enabled() || e.Usage == nil {
		return
	}
	c.goalDriverMu.Lock()
	reservation := c.goalDriverActive
	c.goalDriverMu.Unlock()
	if reservation == nil {
		return
	}
	if _, admitted := reservation.admittedView(); !admitted {
		return
	}
	c.goalResourceMu.Lock()
	c.goalTokensUsed += usageTotalTokens(e.Usage)
	c.goalRequestsUsed += e.Usage.RequestCount
	c.goalResourceMu.Unlock()
}

func (c *Controller) goalResourceBudget() (used, limit int, exhausted bool) {
	c.goalResourceMu.Lock()
	defer c.goalResourceMu.Unlock()
	return c.goalTokensUsed, c.goalTokenLimit, c.goalTokenLimit > 0 && c.goalTokensUsed >= c.goalTokenLimit
}

func (c *Controller) resetGoalResourceBudget() {
	c.goalResourceMu.Lock()
	c.goalTokensUsed = 0
	c.goalRequestsUsed = 0
	c.goalTokenLimit = c.goalTokenBudget
	c.goalBudgetExtensions = 0
	c.goalResourceMu.Unlock()
}

func (c *Controller) goalRoundEligibility() (*goaldomain.View, *sessionv3.Runtime, sessionv3.RuntimeSnapshot, bool) {
	if c == nil || c.PendingPrompt() || c.hasPendingUserWork() {
		return nil, nil, sessionv3.RuntimeSnapshot{}, false
	}
	c.mu.Lock()
	busy := c.running || c.finishing || c.rotating || c.canceling || c.closed
	c.mu.Unlock()
	if busy {
		return nil, nil, sessionv3.RuntimeSnapshot{}, false
	}
	view, err := c.goalLifecycleView()
	if err != nil || view == nil || view.Phase != goaldomain.PhaseActive || view.Activation != goaldomain.ActivationArmed {
		return nil, nil, sessionv3.RuntimeSnapshot{}, false
	}
	_, runtime, exclusive := c.v3Binding()
	if !exclusive || runtime == nil {
		return nil, nil, sessionv3.RuntimeSnapshot{}, false
	}
	snapshot := runtime.Snapshot()
	if snapshot.Phase != sessionv3.RuntimeIdle {
		return nil, nil, sessionv3.RuntimeSnapshot{}, false
	}
	return view, runtime, snapshot, true
}

// commitGoalRoundAdmission persists the turn/start fact and incremented goal
// snapshot in the same logical v3 batch. Only after Append is accepted does it
// publish the candidate machine and expose goal-round tool authority.
func (c *Controller) commitGoalRoundAdmission(reservation *goalRoundReservation) error {
	if reservation == nil {
		return errors.New("missing goal round reservation")
	}
	c.goalLifecycleMutationMu.Lock()
	defer c.goalLifecycleMutationMu.Unlock()
	_, runtime, exclusive := c.v3Binding()
	if !exclusive || runtime == nil {
		return sessionv3.ErrSessionNotRunning
	}
	runtimeSnapshot := runtime.Snapshot()
	if runtimeSnapshot.Phase != sessionv3.RuntimeRunning || runtimeSnapshot.Ref.SessionID != reservation.SessionID ||
		runtimeSnapshot.Epoch != reservation.RuntimeEpoch || runtimeSnapshot.ActivityRevision != reservation.IdleActivityRevision+1 {
		return &goaldomain.Error{Code: goaldomain.ErrStaleRevision, Message: "goal round runtime reservation is stale"}
	}
	c.goalLifecycleMu.RLock()
	machine, loadErr := c.goalLifecycle, c.goalLifecycleLoadErr
	c.goalLifecycleMu.RUnlock()
	if loadErr != nil {
		return loadErr
	}
	if machine == nil {
		return sessionv3.ErrSessionNotRunning
	}
	candidate := machine.Clone()
	view, err := candidate.AdmitRound(reservation.Goal)
	if err != nil {
		return err
	}
	if view.RoundsStarted != reservation.Round {
		return &goaldomain.Error{Code: goaldomain.ErrStaleRevision, Message: "goal round number changed before admission"}
	}
	payload, err := candidate.Encode()
	if err != nil {
		return err
	}
	if err := c.emitTurnEventChecked(event.Event{
		Kind: event.TurnStarted, Status: event.TurnInProgress,
		DomainKind: "goal/state", DomainPayload: payload,
	}); err != nil {
		return err
	}
	c.goalLifecycleMu.Lock()
	if c.goalLifecycle != machine || c.goalLifecycleLoadErr != nil {
		c.goalLifecycleMu.Unlock()
		return &goaldomain.Error{Code: goaldomain.ErrStaleRevision, Message: "goal lifecycle changed during round admission"}
	}
	c.goalLifecycle = candidate
	c.goalLifecycleMu.Unlock()
	reservation.setAdmitted(view)
	c.refreshRuntimeState(event.Event{})
	return nil
}

func (c *Controller) finishGoalRoundActivity(reservation *goalRoundReservation) {
	if reservation == nil {
		return
	}
	c.goalDriverMu.Lock()
	if c.goalDriverActive == reservation {
		c.goalDriverActive = nil
	}
	c.goalDriverMu.Unlock()
	if _, admitted := reservation.admittedView(); !admitted {
		return
	}
	runErr, cancelled := reservation.result()
	if cancelled || errors.Is(runErr, context.Canceled) {
		current, viewErr := c.goalLifecycleView()
		if viewErr != nil || current == nil || current.ID != reservation.Goal.ID || current.Phase != goaldomain.PhaseActive {
			// A terminal goal action accepted before cancellation already owns the
			// outcome. Do not rewrite complete/blocked into a cancellation pause.
			return
		}
		_, err := c.applyHostGoalMutation(context.Background(), "cancelled-goal-round", func(machine *goaldomain.Machine) (*goaldomain.View, error) {
			paused, pauseErr := machine.Pause(current.Ref())
			return &paused, pauseErr
		})
		if err != nil {
			c.disarmGoalLifecycle("cancelled")
		}
		return
	}
	if runErr != nil {
		c.disarmGoalLifecycle("model-error")
	}
}

func (c *Controller) goalAuthorityForRound(reservation *goalRoundReservation) (tool.GoalAuthority, bool) {
	view, ok := reservation.admittedView()
	if !ok {
		return tool.GoalAuthority{}, false
	}
	_, runtime, exclusive := c.v3Binding()
	if !exclusive || runtime == nil {
		return tool.GoalAuthority{}, false
	}
	snapshot := runtime.Snapshot()
	if snapshot.Phase != sessionv3.RuntimeRunning || snapshot.Ref.SessionID != reservation.SessionID || snapshot.Epoch != reservation.RuntimeEpoch {
		return tool.GoalAuthority{}, false
	}
	return tool.GoalAuthority{Source: tool.GoalSourceGoalRound, SessionID: reservation.SessionID,
		RuntimeEpoch: reservation.RuntimeEpoch, ActivityID: snapshot.ActivityRevision,
		GoalID: view.ID, Revision: view.Revision, Round: view.RoundsStarted}, true
}

func (c *Controller) directHumanGoalAuthority() (tool.GoalAuthority, bool) {
	_, runtime, exclusive := c.v3Binding()
	if !exclusive || runtime == nil {
		return tool.GoalAuthority{}, false
	}
	snapshot := runtime.Snapshot()
	if snapshot.Phase != sessionv3.RuntimeRunning {
		return tool.GoalAuthority{}, false
	}
	return tool.GoalAuthority{Source: tool.GoalSourceDirectHuman, SessionID: snapshot.Ref.SessionID,
		RuntimeEpoch: snapshot.Epoch, ActivityID: snapshot.ActivityRevision}, true
}

func (c *Controller) disarmGoalLifecycle(reason string) {
	c.goalLifecycleMutationMu.Lock()
	defer c.goalLifecycleMutationMu.Unlock()
	c.goalLifecycleMu.RLock()
	machine := c.goalLifecycle
	c.goalLifecycleMu.RUnlock()
	if machine != nil {
		machine.Disarm(strings.TrimSpace(reason))
		c.refreshRuntimeState(event.Event{})
	}
}

// applyHostGoalMutation is the serialized UI/command control plane. It uses
// the current activity when one exists or a short owned control activity while
// idle; it never bypasses v3 with a direct sidecar write.
func (c *Controller) applyHostGoalMutation(ctx context.Context, reason string, mutate func(*goaldomain.Machine) (*goaldomain.View, error)) (*goaldomain.View, error) {
	if c == nil || mutate == nil {
		return nil, sessionv3.ErrSessionNotRunning
	}
	c.goalLifecycleMutationMu.Lock()
	defer c.goalLifecycleMutationMu.Unlock()
	c.goalLifecycleMu.RLock()
	machine, loadErr := c.goalLifecycle, c.goalLifecycleLoadErr
	c.goalLifecycleMu.RUnlock()
	if loadErr != nil {
		return nil, loadErr
	}
	if machine == nil {
		return nil, sessionv3.ErrSessionNotRunning
	}
	candidate := machine.Clone()
	view, err := mutate(candidate)
	if err != nil {
		return nil, err
	}
	payload, err := candidate.Encode()
	if err != nil {
		return nil, err
	}
	_, runtime, exclusive := c.v3Binding()
	if !exclusive || runtime == nil {
		return nil, sessionv3.ErrSessionNotRunning
	}
	snapshot := runtime.Snapshot()
	var activity *sessionv3.Activity
	owned := false
	switch snapshot.Phase {
	case sessionv3.RuntimeIdle:
		_, activity, err = runtime.BeginOwnedActivity(ctx, "goal-control")
		if err != nil {
			return nil, err
		}
		owned = true
	case sessionv3.RuntimeRunning:
		c.v3ActivityMu.Lock()
		activity = c.v3Activity
		c.v3ActivityMu.Unlock()
		if activity == nil {
			return nil, sessionv3.ErrStaleActivity
		}
	default:
		return nil, sessionv3.ErrRuntimeBusy
	}
	if owned {
		defer activity.Finish(nil)
	}
	op := fmt.Sprintf("goal-control:%s:%d:%s", reason, runtime.Session().Snapshot().EventSequence+1, snapshot.Epoch)
	if _, err := activity.Append(ctx, sessionv3.Batch{OperationID: op, TurnID: runtime.Session().Snapshot().Projection.TurnID,
		Events: []sessionv3.Event{{Kind: "goal/state", Payload: payload}}}); err != nil {
		return nil, err
	}
	_, currentRuntime, stillExclusive := c.v3Binding()
	if !stillExclusive || currentRuntime != runtime {
		return view, nil
	}
	c.goalLifecycleMu.Lock()
	if c.goalLifecycle == machine && c.goalLifecycleLoadErr == nil {
		c.goalLifecycle = candidate
	}
	c.goalLifecycleMu.Unlock()
	c.refreshRuntimeState(event.Event{})
	return view, nil
}
