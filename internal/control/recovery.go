package control

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/recovery"
)

// SetRecoveryCheckpointEnabled arms or disarms the Auto-mode failure recovery
// checkpoint for this session. The preference is retained under Ask/YOLO but
// only takes effect while the tool approval mode is Auto.
func (c *Controller) SetRecoveryCheckpointEnabled(enabled bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.recoveryEnabled = enabled
	gate := c.recoveryGate
	c.mu.Unlock()
	if gate != nil {
		gate.SetEnabled(enabled)
	}
	c.persistRecoveryEnabled(enabled)
}

// SetRecoveryCheckpointDefaultEnabled changes the default sampled only when
// this controller rotates to a fresh session. It deliberately leaves the
// current session preference untouched.
func (c *Controller) SetRecoveryCheckpointDefaultEnabled(enabled bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.recoveryDefaultEnabled = enabled
	c.mu.Unlock()
}

// RecoveryCheckpointEnabled reports the session preference (not whether Auto
// is currently active).
func (c *Controller) RecoveryCheckpointEnabled() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.recoveryEnabled
}

// ResolveRecovery applies a user decision on a recovery confirmation card.
// action is continue|revise|stop. For revise, feedback is returned in the
// blocked tool result so the same agent sees it exactly once before retrying.
func (c *Controller) ResolveRecovery(id string, action agent.RecoveryAction, feedback string) error {
	if c == nil {
		return fmt.Errorf("controller is nil")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("empty recovery approval id")
	}
	switch action {
	case agent.RecoveryActionContinue, agent.RecoveryActionRevise, agent.RecoveryActionStop:
	default:
		// Accept plain strings from wire clients.
		switch strings.ToLower(strings.TrimSpace(string(action))) {
		case "continue":
			action = agent.RecoveryActionContinue
		case "revise":
			action = agent.RecoveryActionRevise
		case "stop":
			action = agent.RecoveryActionStop
		default:
			return fmt.Errorf("unknown recovery action %q", action)
		}
	}

	// Also resolve any matching approvalManager entry so legacy Approve paths
	// and ReplayPending do not keep a stale prompt.
	pending := c.approval.resolve(id)
	if pending.reply != nil {
		switch action {
		case agent.RecoveryActionContinue:
			pending.reply <- approvalReply{allow: true}
		default:
			pending.reply <- approvalReply{allow: false}
		}
	}

	c.mu.Lock()
	gate := c.recoveryGate
	c.mu.Unlock()
	if gate == nil {
		return fmt.Errorf("recovery checkpoint is not active")
	}
	return gate.Resolve(id, recovery.Action(action), feedback)
}

// initRecoveryGate constructs the shared recovery gate and attaches it to the
// executor. Called from New when recovery is available.
func (c *Controller) initRecoveryGate(enabled bool, reviewer recovery.Reviewer, headless bool) {
	if c == nil || c.executor == nil {
		return
	}
	gate := recovery.NewGate(recovery.Options{
		Enabled:  enabled,
		Headless: headless,
		Mode: func() string {
			return c.ToolApprovalMode()
		},
		EmitPrompt: c.emitRecoveryPrompt,
		Reviewer:   reviewer,
		Cancel: func() {
			c.Cancel()
		},
		PersistenceKey: c.SessionPath,
		Persist: func(path string, snap recovery.Snapshot) {
			c.persistRecoverySnapshot(path, snap)
		},
		TaskSummary: func() string {
			if c.executor == nil || c.executor.Session() == nil {
				return ""
			}
			msgs := c.executor.Session().Snapshot()
			for i := len(msgs) - 1; i >= 0; i-- {
				if string(msgs[i].Role) == "user" && strings.TrimSpace(msgs[i].Content) != "" {
					text := strings.TrimSpace(msgs[i].Content)
					if len(text) > 800 {
						return text[:800] + "…"
					}
					return text
				}
			}
			return ""
		},
	})
	c.mu.Lock()
	c.recoveryGate = gate
	c.recoveryEnabled = enabled
	c.mu.Unlock()
	c.executor.SetRecoveryGate(gate)
}

func (c *Controller) persistRecoverySnapshot(path string, snap recovery.Snapshot) {
	if c == nil {
		return
	}
	if strings.TrimSpace(path) == "" {
		return
	}
	if err := recovery.SaveSnapshot(path, snap); err != nil {
		slog.Warn("controller: recovery snapshot", "err", err)
	}
}

// loadRecoveryState restores the recovery gate sidecar for a session path.
func (c *Controller) loadRecoveryState(path string) {
	if c == nil {
		return
	}
	c.approval.clearKind(recovery.ApprovalKindRecovery)
	c.mu.Lock()
	gate := c.recoveryGate
	c.mu.Unlock()
	if gate != nil {
		snap := recovery.Snapshot{}
		if strings.TrimSpace(path) != "" {
			loaded, err := recovery.LoadSnapshot(path)
			if err != nil {
				slog.Warn("controller: load recovery snapshot", "err", err)
			} else {
				snap = loaded
			}
		}
		// Missing, empty, or unreadable sidecars must still replace the old
		// in-memory state; otherwise a session switch carries its checkpoint.
		gate.Restore(snap)
	}
}

// resetRecoveryForNewSession clears any failure checkpoint inherited from the
// previous path and reapplies the configured new-session default. Metadata is
// not created here: richer frontends still need to attach topic/scope ownership
// before the first sidecar write, and ordinary snapshots persist the preference.
func (c *Controller) resetRecoveryForNewSession(path string) {
	if c == nil {
		return
	}
	c.loadRecoveryState(path)
	c.mu.Lock()
	enabled := c.recoveryDefaultEnabled
	c.recoveryEnabled = enabled
	gate := c.recoveryGate
	c.mu.Unlock()
	if gate != nil {
		gate.SetEnabled(enabled)
	}
}

// carryRecoveryState moves a tip branch onto a new session identity without
// carrying live approval channels or one-shot grants across the boundary.
func (c *Controller) carryRecoveryState(path string) {
	if c == nil {
		return
	}
	c.approval.clearKind(recovery.ApprovalKindRecovery)
	c.mu.Lock()
	gate := c.recoveryGate
	c.mu.Unlock()
	if gate == nil {
		return
	}
	gate.Restore(gate.Snapshot())
	c.saveRecoveryState(path)
}

func (c *Controller) flushRecoveryPersistence(path string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	gate := c.recoveryGate
	c.mu.Unlock()
	if gate != nil {
		gate.FlushPersistence(path)
	}
}

// saveRecoveryState persists the recovery gate sidecar. The independent
// reviewer resets to its fixed system prompt before every review, so persisting
// its transient conversation adds no cache warmth and only creates a second
// transcript-shaped file beside the real session.
func (c *Controller) saveRecoveryState(path string) {
	if c == nil || strings.TrimSpace(path) == "" {
		return
	}
	c.mu.Lock()
	gate := c.recoveryGate
	c.mu.Unlock()
	if gate != nil {
		if err := recovery.SaveSnapshot(path, gate.Snapshot()); err != nil {
			slog.Warn("controller: recovery snapshot", "err", err)
		}
	}
}

// RecoveryMetrics returns content-free recovery counters for export/observation.
func (c *Controller) RecoveryMetrics() recovery.Metrics {
	if c == nil {
		return recovery.Metrics{}
	}
	c.mu.Lock()
	gate := c.recoveryGate
	c.mu.Unlock()
	if gate == nil {
		return recovery.Metrics{}
	}
	return gate.Metrics()
}

// ReplayUnresolvedRecoveries re-emits recovery cards for active failures that
// still have a pending proposal (e.g. after a frontend reconnect). Live waiters
// remain owned by BeforeMutation; this only restores the UI card via the same
// ApprovalRequest infrastructure as ordinary pending prompts.
func (c *Controller) ReplayUnresolvedRecoveries() {
	if c == nil {
		return
	}
	c.mu.Lock()
	gate := c.recoveryGate
	c.mu.Unlock()
	if gate == nil || !gate.Enabled() || c.ToolApprovalMode() != ToolApprovalAuto {
		return
	}
	// Prefer ordinary ReplayPendingPrompts when the approval manager still has
	// the live prompt (in-process reconnect). Only synthesize cards for
	// restored failures that still carry a pending proposal and have no live id.
	for _, item := range gate.UnresolvedForReplay() {
		if item.Pending == nil || strings.TrimSpace(item.Pending.Fingerprint) == "" {
			continue
		}
		// Skip if a live approval is already pending for this recovery.
		if c.approval.hasPending() {
			continue
		}
		pending := *item.Pending
		ev := recovery.ToEventApproval("", pending, &item.Failure)
		id, _ := c.approval.registerDecisionKind(
			pending.Tool,
			recoveryFirstNonEmpty(pending.Subject, pending.Tool),
			recoveryFirstNonEmpty(pending.Rationale, "recovery checkpoint"),
			true,
			recovery.ApprovalKindRecovery,
			ev.Recovery,
		)
		ev.ID = id
		gate.BindApprovalID(item.TaskID, id)
		c.sink.Emit(c.approvalRequestEvent(ev))
	}
}

func (c *Controller) emitRecoveryPrompt(ctx context.Context, taskID string, pending recovery.PendingProposal, failure *recovery.FailureEvent) (string, error) {
	if c == nil {
		return "", fmt.Errorf("controller is nil")
	}
	// Strict fresh decision: never session/persist grants, never auto-drain on
	// mode switch.
	c.approval.promptMu.Lock()
	// Hold promptMu for the duration of registration+emit only; waiting happens
	// in the recovery gate on its own channel. We deliberately do not block here
	// on the approval reply — ResolveRecovery unblocks the gate.
	ev := recovery.ToEventApproval("", pending, failure)
	id, reply := c.approval.registerDecisionKind(
		pending.Tool,
		recoveryFirstNonEmpty(pending.Subject, pending.Tool),
		recoveryFirstNonEmpty(pending.Rationale, "recovery checkpoint"),
		true,
		recovery.ApprovalKindRecovery,
		ev.Recovery,
	)
	ev.ID = id
	c.mu.Lock()
	gate := c.recoveryGate
	c.mu.Unlock()
	if gate != nil {
		// Bind before Emit: some sinks synchronously resolve the event from
		// inside Emit, so binding afterwards loses that decision.
		gate.BindApprovalID(taskID, id)
	}
	// Drain the ordinary approval reply when ResolveRecovery/Approve fires so
	// the channel never leaks; the gate is the real waiter.
	go func() {
		select {
		case <-reply:
		case <-ctx.Done():
			c.approval.cancel(id)
		}
	}()

	c.sink.Emit(c.approvalRequestEvent(ev))
	c.approval.promptMu.Unlock()

	if c.hooks != nil {
		go c.hooks.Notification(ctx, "Recovery checkpoint: confirm the next change", "permission_prompt")
	}
	return id, nil
}

// loadRecoveryEnabled restores the per-session preference. Legacy metadata or
// a missing field is deliberately false; config defaults only seed new files.
func (c *Controller) loadRecoveryEnabled(path string) {
	enabled := false
	if strings.TrimSpace(path) != "" {
		meta, ok, err := agent.LoadBranchMeta(path)
		if err != nil {
			slog.Warn("controller: load recovery preference", "err", err)
		} else if ok && meta.RecoveryCheckpointEnabled != nil {
			enabled = *meta.RecoveryCheckpointEnabled
		}
	}
	c.mu.Lock()
	c.recoveryEnabled = enabled
	gate := c.recoveryGate
	c.mu.Unlock()
	if gate != nil {
		gate.SetEnabled(enabled)
	}
}

func (c *Controller) persistRecoveryEnabled(enabled bool) {
	if c == nil {
		return
	}
	path := c.SessionPath()
	if strings.TrimSpace(path) == "" {
		return
	}
	unlock := agent.LockSessionMetaPath(path)
	defer unlock()
	meta, ok, err := agent.LoadBranchMeta(path)
	if err != nil {
		return
	}
	if !ok {
		meta = agent.BranchMeta{}
	}
	v := enabled
	meta.RecoveryCheckpointEnabled = &v
	_ = agent.SaveBranchMetaPreserveUpdated(path, meta)
}

func recoveryFirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
