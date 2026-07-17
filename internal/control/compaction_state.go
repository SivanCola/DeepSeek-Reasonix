package control

import (
	"fmt"
	"sort"
	"strings"

	"reasonix/internal/agent"
)

// CompactionState implements agent.CompactionStateProvider for the Controller.
// It snapshots Goal/Todo/Job/path state deterministically for post-compaction
// recovery; the model never summarizes this block.
func (c *Controller) CompactionState() agent.CompactionState {
	if c == nil {
		return agent.CompactionState{}
	}
	st := agent.CompactionState{}

	goal, status, _, _ := c.goals.snapshot()
	st.ActiveGoal = goal
	st.GoalStatus = status
	if status == GoalStatusBlocked {
		st.BlockedGoal = goal
		// Persist any host-visible block reason from the goal machine state file
		// path or last known intercept when available.
		if reason := c.goals.blockReasonText(); reason != "" {
			st.BlockReason = reason
		}
	}

	todos := c.Todos()
	for i, t := range todos {
		status := strings.ToLower(strings.TrimSpace(t.Status))
		switch status {
		case "completed", "done":
			st.CompletedTodos++
		case "cancelled", "canceled":
			st.CancelledTodos++
		default:
			st.Todos = append(st.Todos, agent.CompactionTodo{
				ID:     fmt.Sprintf("%d", i+1),
				Status: t.Status,
				Text:   t.Content,
			})
		}
	}

	// Running background jobs / sub-agents for this session.
	if c.jobs != nil {
		parent := c.parentSessionID()
		for _, v := range c.jobs.RunningForSession(parent) {
			st.Jobs = append(st.Jobs, agent.CompactionJob{
				ID:    v.ID,
				Kind:  v.Kind,
				Label: v.Label,
			})
		}
	}

	// Edited paths: checkpoint store + evidence receipts union.
	pathSet := map[string]struct{}{}
	if c.checkpoints.enabled() {
		for _, p := range c.checkpoints.editedPaths() {
			if p = strings.TrimSpace(p); p != "" {
				pathSet[p] = struct{}{}
			}
		}
	}
	if c.executor != nil {
		for _, p := range c.executor.EvidenceEditedPaths() {
			if p = strings.TrimSpace(p); p != "" {
				pathSet[p] = struct{}{}
			}
		}
	}
	for p := range pathSet {
		st.EditedPaths = append(st.EditedPaths, p)
	}
	sort.Strings(st.EditedPaths)

	if cp := c.goals.deliveryState(); cp.CriteriaEstablished || cp.WorkObserved || cp.MutationObserved {
		var parts []string
		if cp.CriteriaEstablished {
			parts = append(parts, "criteria")
		}
		if cp.WorkObserved {
			parts = append(parts, "work")
		}
		if cp.MutationObserved {
			parts = append(parts, "mutation")
		}
		if cp.PendingMutation {
			parts = append(parts, "pending_mutation")
		}
		st.DeliveryCP = strings.Join(parts, ",")
	}

	return st
}
