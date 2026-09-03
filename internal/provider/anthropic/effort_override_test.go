package anthropic

import (
	"context"
	"testing"

	"reasonix/internal/provider"
)

// Per-request EffortOverride on the DeepSeek Anthropic endpoint: a bounded
// reviewer's stateless call can disable thinking or pick a depth without
// touching the client-level configuration the main loop relies on.
func TestBuildRequestDeepSeekEffortOverride(t *testing.T) {
	for _, tc := range []struct {
		name       string
		override   string
		wantThink  string
		wantEffort string // "" = no output_config
	}{
		{name: "no override keeps client defaults", override: "", wantThink: "enabled", wantEffort: "high"},
		{name: "disabled turns thinking off", override: "disabled", wantThink: "disabled"},
		{name: "disabled with surrounding space", override: " Disabled ", wantThink: "disabled"},
		{name: "depth replaces configured effort", override: "low", wantThink: "enabled", wantEffort: "low"},
		{name: "max is a valid depth", override: "max", wantThink: "enabled", wantEffort: "max"},
		{name: "medium normalizes to high", override: "medium", wantThink: "enabled", wantEffort: "high"},
		{name: "unknown value falls back", override: "extreme", wantThink: "enabled", wantEffort: "high"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &client{model: "deepseek-v4-flash", deepseek: true, thinking: "enabled", effort: "high"}
			r := c.buildRequest(context.Background(), provider.Request{
				Messages:       []provider.Message{{Role: provider.RoleUser, Content: "judge this"}},
				EffortOverride: tc.override,
			})
			if r.Thinking == nil || r.Thinking.Type != tc.wantThink {
				t.Fatalf("thinking = %+v, want type %q", r.Thinking, tc.wantThink)
			}
			var gotEffort string
			if r.OutputConfig != nil {
				gotEffort = r.OutputConfig.Effort
			}
			if gotEffort != tc.wantEffort {
				t.Fatalf("output_config.effort = %q, want %q", gotEffort, tc.wantEffort)
			}
			if c.thinking != "enabled" || c.effort != "high" {
				t.Fatalf("client config mutated: thinking=%q effort=%q", c.thinking, c.effort)
			}
		})
	}
}

// A per-request override may only lower or disable thinking: with the client
// configured thinking-off, no override re-enables it.
func TestBuildRequestDeepSeekEffortOverrideCannotReenableThinking(t *testing.T) {
	for _, c := range []*client{
		{model: "deepseek-v4-flash", deepseek: true, thinking: "disabled"},
		{model: "deepseek-v4-flash", deepseek: true, thinking: "enabled", effort: "disabled"},
	} {
		r := c.buildRequest(context.Background(), provider.Request{EffortOverride: "high"})
		if r.Thinking == nil || r.Thinking.Type != "disabled" {
			t.Fatalf("thinking = %+v, want disabled (client config wins over a depth override)", r.Thinking)
		}
		if r.OutputConfig != nil {
			t.Fatalf("output_config = %+v, want omitted while thinking is off", r.OutputConfig)
		}
	}
}

// The missing-reasoning recovery already forces thinking off; a per-request
// depth override must not re-enable it or emit output_config.
func TestBuildRequestDeepSeekRecoveryIgnoresEffortOverride(t *testing.T) {
	c := &client{model: "deepseek-v4-flash", deepseek: true, thinking: "enabled", effort: "high"}
	ctx := provider.WithMissingReasoningFallback(context.Background())
	r := c.buildRequest(ctx, provider.Request{
		Messages:       []provider.Message{{Role: provider.RoleUser, Content: "do it"}},
		EffortOverride: "max",
	})
	if r.Thinking == nil || r.Thinking.Type != "disabled" || r.OutputConfig != nil {
		t.Fatalf("recovery with override: thinking = %+v output_config = %+v, want disabled/none", r.Thinking, r.OutputConfig)
	}
}
