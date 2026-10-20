package scheduler

import (
	"fmt"
	"time"

	"reasonix/internal/contract/config"
	"reasonix/internal/state/schedule"
)

// PinModel fixes the model a schedule will use from the user's own
// configuration, so a repository cannot change which model, or whose key, a
// confirmed run uses. An empty ref is the user's default model.
func PinModel(ref, workspace string) (schedule.Model, error) {
	cfg, err := config.RootsForHome("").LoadUserScopeForRoot(workspace)
	if err != nil {
		return schedule.Model{}, err
	}
	if ref == "" {
		ref = cfg.DefaultModel
	}
	entry, ok := cfg.ResolveModel(ref)
	if !ok || entry == nil {
		return schedule.Model{}, fmt.Errorf("model %q is not a configured model", ref)
	}
	return schedule.Model{Provider: entry.Name, Model: entry.Model}, nil
}

// Facts is what a person confirms before a schedule exists: when it first runs,
// where it works, which model, and the most it can spend, in the policy's own
// numbers. Every frontend shows these and none computes them itself.
type Facts struct {
	Trigger   schedule.Trigger
	FirstRun  time.Time
	Folder    string
	Model     schedule.Model
	Prompt    string
	Budget    schedule.Budget
	PerDayMax int64
	Lifetime  int64
	// ExpiresAt is when it ends on its own unless a person renews it.
	ExpiresAt time.Time
}

// FactsFor computes the confirmation facts for a request under a policy.
func FactsFor(req schedule.CreateRequest, p schedule.Policy, now time.Time) Facts {
	b := p.DefaultBudget()
	if req.Budget != nil {
		b = *req.Budget
	}
	f := Facts{Trigger: req.Trigger, Folder: req.Target.Workspace, Model: req.Model, Prompt: req.Prompt, Budget: b,
		PerDayMax: min(b.PerRunTokens*b.MaxRunsPerDay, p.GlobalTokensDay), Lifetime: b.LifetimeTokens, ExpiresAt: now.Add(p.Expiry())}
	switch req.Trigger.Kind {
	case schedule.TriggerEvery:
		f.FirstRun = now.Add(time.Duration(req.Trigger.EverySec) * time.Second)
	case schedule.TriggerAt:
		f.FirstRun = req.Trigger.At
	}
	return f
}
