package daemon

import (
	"fmt"
	"time"

	"reasonix/internal/agent"
)

func reserveAutoWakeupBudget(runtime *agent.RuntimeMeta, source string, now time.Time) (bool, string) {
	if runtime == nil {
		return false, "runtime missing"
	}
	now = now.UTC()
	budget := &runtime.Budget
	window := budgetWindowStart(now)
	if budget.WindowStartedAt.IsZero() || !budget.WindowStartedAt.Equal(window) {
		budget.WindowStartedAt = window
		budget.DailyWakeups = 0
	}
	if budget.DailyWakeupLimit > 0 && budget.DailyWakeups >= budget.DailyWakeupLimit {
		reason := fmt.Sprintf("daily automatic wakeup budget exhausted for %s (%d/%d)", source, budget.DailyWakeups, budget.DailyWakeupLimit)
		budget.LastBlockedAt = now
		budget.LastBlockedReason = reason
		return false, reason
	}
	budget.DailyWakeups++
	return true, ""
}

func budgetWindowStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
