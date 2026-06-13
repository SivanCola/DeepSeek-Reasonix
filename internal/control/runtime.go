package control

import (
	"context"
	"log/slog"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/i18n"
)

// RuntimeSnapshot builds a RuntimeMeta from the controller's current state.
// It captures the active goal, run status, and turn metadata — everything needed
// to restore a session's runtime posture on resume.
func (c *Controller) RuntimeSnapshot() agent.RuntimeMeta {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runtimeSnapshotLocked()
}

func (c *Controller) runtimeSnapshotLocked() agent.RuntimeMeta {
	m := agent.RuntimeMeta{
		SessionID:     agent.BranchID(c.sessionPath),
		Model:         c.label,
		WorkspaceRoot: c.cpRoot,
	}

	// Goal state.
	if c.goal != "" {
		m.Goal = agent.RuntimeGoalMeta{
			Text:        c.goal,
			Status:      c.goalStatus,
			Turns:       c.goalTurns,
			BlockCount:  c.goalBlocks,
			BlockReason: c.goalBlock,
			UpdatedAt:   time.Now().UTC(),
		}
	} else if c.goalStatus == GoalStatusComplete {
		// Goal completed: preserve status but clear text to prevent accidental re-run.
		m.Goal = agent.RuntimeGoalMeta{
			Status:    GoalStatusComplete,
			Turns:     c.goalTurns,
			UpdatedAt: time.Now().UTC(),
		}
	}

	// Run state.
	runStatus := "idle"
	if c.running {
		runStatus = "running"
	}
	m.Run = agent.RuntimeRunMeta{
		Status:     runStatus,
		LastTurnAt: time.Now().UTC(),
	}

	return m
}

func mergeRuntimeForSave(path string, next agent.RuntimeMeta) agent.RuntimeMeta {
	prev, ok, err := agent.LoadRuntimeMeta(path)
	if err != nil || !ok {
		return next
	}
	next.Scheduler = prev.Scheduler
	next.FileWatch = prev.FileWatch
	next.Budget = prev.Budget
	if next.Wait.Kind == "" && prev.Wait.Kind == "event" {
		next.Wait = prev.Wait
	}
	if next.Model == "" {
		next.Model = prev.Model
	}
	if next.WorkspaceRoot == "" {
		next.WorkspaceRoot = prev.WorkspaceRoot
	}
	if next.Run.ResumeCount == 0 {
		next.Run.ResumeCount = prev.Run.ResumeCount
	}
	if next.Run.LastWakeupReason == "" {
		next.Run.LastWakeupReason = prev.Run.LastWakeupReason
	}
	if next.Run.LastError == "" {
		next.Run.LastError = prev.Run.LastError
	}
	return next
}

func hasPersistentRuntimeConfig(m agent.RuntimeMeta) bool {
	return m.Scheduler.Enabled ||
		m.Scheduler.DailyAt != "" ||
		m.Scheduler.Interval > 0 ||
		m.FileWatch.Enabled ||
		len(m.FileWatch.Paths) > 0 ||
		m.Budget.DailyWakeupLimit > 0
}

// RestoreRuntimeSnapshot applies a previously-saved RuntimeMeta to the
// controller, restoring goal and run bookkeeping. It does NOT trigger any
// model calls — resume is always passive.
func (c *Controller) RestoreRuntimeSnapshot(m agent.RuntimeMeta) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Restore goal state.
	c.goal = m.Goal.Text
	c.goalStatus = m.Goal.Status
	c.goalTurns = m.Goal.Turns
	c.goalBlocks = m.Goal.BlockCount
	c.goalBlock = m.Goal.BlockReason

	// If the run was interrupted (e.g. crash while running), mark it so the
	// user/bot can see what happened. Don't auto-resume.
	if m.Run.Status == "running" {
		// The session was likely killed mid-flight — mark interrupted.
		m.Run.Status = "interrupted"
	}
}

// hasActiveGoal reports whether the controller has a goal that warrants runtime
// persistence (running or blocked — complete goals are kept briefly for
// visibility, stopped goals are not persisted).
func (c *Controller) hasActiveGoalLocked() bool {
	if c.goal != "" && (c.goalStatus == GoalStatusRunning || c.goalStatus == GoalStatusBlocked) {
		return true
	}
	// Also persist "just completed" so the status is visible on next resume.
	if c.goalStatus == GoalStatusComplete {
		return true
	}
	return false
}

// saveRuntimeSidecar persists the runtime state if there is an active goal.
// Called from snapshot() after the transcript is saved.
func (c *Controller) saveRuntimeSidecar(path string) {
	c.mu.Lock()
	active := c.hasActiveGoalLocked()
	meta := c.runtimeSnapshotLocked()
	c.mu.Unlock()

	if !active {
		prev, ok, err := agent.LoadRuntimeMeta(path)
		if err != nil || !ok {
			return
		}
		if hasPersistentRuntimeConfig(prev) {
			meta.Scheduler = prev.Scheduler
			meta.FileWatch = prev.FileWatch
			meta.Budget = prev.Budget
			if err := agent.SaveRuntimeMeta(path, meta); err != nil {
				slog.Warn("controller: clear runtime goal state", "err", err)
			}
			return
		}
		if err := agent.RemoveRuntimeMeta(path); err != nil {
			slog.Warn("controller: remove runtime sidecar", "err", err)
		}
		return
	}

	meta = mergeRuntimeForSave(path, meta)
	if err := agent.SaveRuntimeMeta(path, meta); err != nil {
		slog.Warn("controller: save runtime sidecar", "err", err)
	}
}

// loadAndRestoreRuntime loads the runtime sidecar and applies it. Returns true
// if a sidecar was found and restored. Errors are logged as warnings but do
// not block session resume.
func (c *Controller) loadAndRestoreRuntime(path string) bool {
	m, ok, err := agent.LoadRuntimeMeta(path)
	if err != nil {
		slog.Warn("controller: load runtime sidecar", "err", err, "path", path)
		return false
	}
	if !ok {
		return false
	}
	c.RestoreRuntimeSnapshot(m)
	return true
}

// ContinueGoal explicitly resumes work on the active goal. It is the entry
// point for `/goal continue`, bot `/goal continue`, and future daemon wakeups.
// The reason parameter is logged for observability (e.g. "user", "bot", "cron").
//
// Behavior:
//   - No active goal → returns nil (caller emits notice).
//   - Goal running → resets blocked audit if any, starts continuation loop.
//   - Goal blocked → resets blocked state, starts continuation loop.
//   - Goal complete/stopped → returns nil (caller emits notice).
//   - Context cancel → marks goal stopped.
//   - Hits maxGoalAutoTurns → marks blocked, stops.
func (c *Controller) ContinueGoal(ctx context.Context, reason string) error {
	return c.ContinueGoalWithContext(ctx, reason, "")
}

// ContinueGoalWithContext resumes the active goal and injects bounded wakeup
// context into the first continuation turn. The context is dynamic runtime
// state, not part of the stable system prompt.
func (c *Controller) ContinueGoalWithContext(ctx context.Context, reason, wakeupContext string) error {
	ctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		cancel()
		return ErrTurnRunning
	}
	c.cancel = cancel
	c.running = true
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.running = false
		c.cancel = nil
		path := c.sessionPath
		c.mu.Unlock()
		if path != "" {
			c.saveRuntimeSidecar(path)
		}
		cancel()
	}()

	return c.continueGoalWithReason(ctx, reason, wakeupContext)
}

func (c *Controller) continueGoalWithReason(ctx context.Context, reason, wakeupContext string) error {
	c.mu.Lock()
	goal := c.goal
	status := c.goalStatus
	c.mu.Unlock()

	if goal == "" || status == GoalStatusComplete || status == GoalStatusStopped {
		return nil
	}

	// Reset blocked state so the goal can proceed.
	c.mu.Lock()
	if c.goalStatus == GoalStatusBlocked {
		c.goalStatus = GoalStatusRunning
		c.goalBlocks = 0
		c.goalBlock = ""
	}
	c.mu.Unlock()

	// Update runtime sidecar with wakeup info.
	c.mu.Lock()
	path := c.sessionPath
	c.mu.Unlock()
	if path != "" {
		meta := c.RuntimeSnapshot()
		meta = mergeRuntimeForSave(path, meta)
		meta.Run.Status = "running"
		meta.Run.LastWakeupReason = reason
		meta.Run.ResumeCount++
		if err := agent.SaveRuntimeMeta(path, meta); err != nil {
			slog.Warn("controller: save runtime before continue", "err", err)
		}
	}

	// Run the continuation loop — uses goalContinueTurn (not "Start pursuing…").
	err := c.continueGoal(ctx, wakeupContext)

	return err
}

// applyGoalContinue handles the `/goal continue` slash command dispatch.
func (c *Controller) applyGoalContinue() {
	c.mu.Lock()
	goal := c.goal
	status := c.goalStatus
	c.mu.Unlock()

	switch {
	case goal == "" && (status == "" || status == GoalStatusStopped):
		c.notice(i18n.M.GoalNoActive)
	case status == GoalStatusComplete:
		c.notice(i18n.M.GoalAlreadyComplete)
	case status == GoalStatusStopped:
		c.notice(i18n.M.GoalAlreadyStopped)
	default:
		// Goal is running or blocked — continue it.
		c.notice(i18n.M.GoalContinued)
		if c.runner != nil {
			c.runGuarded(func(ctx context.Context) error {
				return c.continueGoalWithReason(ctx, "user", "")
			})
		}
	}
}
