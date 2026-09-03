package openai

import (
	"strings"
	"testing"

	"reasonix/internal/provider"
)

func newTestClient(t *testing.T, model string, extra map[string]any) *client {
	t.Helper()
	cfg := provider.Config{Name: "p", BaseURL: "https://api.deepseek.com", Model: model, APIKey: "k", Extra: extra}
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p.(*client)
}

func TestEffortOverrideDeepSeekFlash(t *testing.T) {
	c := newTestClient(t, "deepseek-v4-flash", map[string]any{"reasoning_protocol": "deepseek"})
	if got := c.buildRequest(provider.Request{}).ReasoningEffort; got != "high" {
		t.Fatalf("default reasoning_effort = %q, want high", got)
	}
	if got := c.buildRequest(provider.Request{EffortOverride: "low"}).ReasoningEffort; got != "low" {
		t.Fatalf("override low: reasoning_effort = %q, want low", got)
	}
	if got := c.buildRequest(provider.Request{EffortOverride: "medium"}).ReasoningEffort; got != "high" {
		t.Fatalf("override outside DeepSeek vocabulary must keep the default, got %q", got)
	}
	out := c.buildRequest(provider.Request{EffortOverride: "disabled"})
	if out.Thinking == nil || out.Thinking.Type != "disabled" || out.ReasoningEffort != "" {
		t.Fatalf("per-request disabled = thinking %+v / effort %q, want thinking.type=disabled with the depth hint dropped",
			out.Thinking, out.ReasoningEffort)
	}
}

func TestEffortOverrideDeepSeekNonFlashRejectsLow(t *testing.T) {
	c := newTestClient(t, "deepseek-v4", map[string]any{"reasoning_protocol": "deepseek"})
	if got := c.buildRequest(provider.Request{EffortOverride: "low"}).ReasoningEffort; got != "high" {
		t.Fatalf("low is flash-only; reasoning_effort = %q, want high", got)
	}
	if got := c.buildRequest(provider.Request{EffortOverride: "max"}).ReasoningEffort; got != "max" {
		t.Fatalf("max is in the official DeepSeek vocabulary, got %q", got)
	}
}

func TestEffortOverrideDeepSeekProSupportsLow(t *testing.T) {
	c := newTestClient(t, "deepseek-v4-pro", map[string]any{"reasoning_protocol": "deepseek"})
	if got := c.buildRequest(provider.Request{EffortOverride: "low"}).ReasoningEffort; got != "low" {
		t.Fatalf("Pro low reasoning_effort = %q, want low", got)
	}
}

func TestEffortOverrideHonorsSupportedEfforts(t *testing.T) {
	c := newTestClient(t, "deepseek-v4", map[string]any{
		"reasoning_protocol": "deepseek",
		"effort":             "high",
		"supported_efforts":  []string{"low", "high", "disabled"},
	})
	if got := c.buildRequest(provider.Request{EffortOverride: "low"}).ReasoningEffort; got != "low" {
		t.Fatalf("declared vocabulary must admit low, got %q", got)
	}
	if got := c.buildRequest(provider.Request{EffortOverride: "max"}).ReasoningEffort; got != "high" {
		t.Fatalf("undeclared level must keep the default, got %q", got)
	}
	out := c.buildRequest(provider.Request{EffortOverride: "disabled"})
	if out.Thinking == nil || out.Thinking.Type != "disabled" || out.ReasoningEffort != "" {
		t.Fatalf("disabled is a per-request thinking toggle, not a depth: thinking %+v / effort %q", out.Thinking, out.ReasoningEffort)
	}
}

// A client already configured thinking-off is unaffected by overrides, and no
// per-request value can turn thinking back on.
func TestEffortOverrideCannotReenableThinking(t *testing.T) {
	c := newTestClient(t, "deepseek-v4", map[string]any{"reasoning_protocol": "deepseek", "thinking": "disabled"})
	for _, override := range []string{"", "low", "high", "enabled"} {
		out := c.buildRequest(provider.Request{EffortOverride: override})
		if out.Thinking == nil || out.Thinking.Type != "disabled" || out.ReasoningEffort != "" {
			t.Fatalf("override %q: thinking %+v / effort %q, want thinking.type=disabled only", override, out.Thinking, out.ReasoningEffort)
		}
	}
}

// Outside DeepSeek the per-request toggle stays inert: binary-knob endpoints
// keep their configured thinking state, so one request on a shared client can
// never switch the main loop's thinking off there.
func TestEffortOverrideDisabledInertOutsideDeepSeek(t *testing.T) {
	for _, c := range []*client{
		{model: "MiniMax-M3", minimax: true},
		{model: "glm-4.5-air", zhipu: true},
		{model: "LongCat-Flash", longcat: true},
		{model: "some-model", thinkingType: "enabled"},
	} {
		out := c.buildRequest(provider.Request{EffortOverride: "disabled"})
		if out.Thinking == nil || out.Thinking.Type == "disabled" {
			t.Errorf("%s: thinking = %+v, want the configured state, not per-request disabled", c.model, out.Thinking)
		}
	}
}

// Endpoints with no per-request thinking knob on the wire leave the override
// inert rather than inventing fields.
func TestEffortOverrideDisabledInertWithoutThinkingKnob(t *testing.T) {
	c := &client{model: "mimo-v2"}
	out := c.buildRequest(provider.Request{EffortOverride: "disabled"})
	if out.Thinking != nil {
		t.Fatalf("thinking = %+v, want omitted on a knob-less endpoint", out.Thinking)
	}
	if out.ReasoningEffort != "" {
		t.Fatalf("reasoning_effort = %q, want omitted", out.ReasoningEffort)
	}
}

func TestEffortOverrideIgnoredByBinaryThinkingKnobs(t *testing.T) {
	for _, c := range []*client{
		{model: "MiniMax-M3", minimax: true},
		{model: "glm-4.5-air", zhipu: true},
		{model: "LongCat-Flash", longcat: true},
	} {
		out := c.buildRequest(provider.Request{EffortOverride: "low"})
		if out.ReasoningEffort != "" {
			t.Errorf("%s: reasoning_effort = %q, want omitted", c.model, out.ReasoningEffort)
		}
		if out.Thinking != nil && strings.Contains(out.Thinking.Type, "low") {
			t.Errorf("%s: override leaked into thinking.type %q", c.model, out.Thinking.Type)
		}
	}
}

func TestEffortOverrideIgnoredWithoutDepthVocabulary(t *testing.T) {
	// A generic gateway with no configured effort and no supported_efforts has
	// no evidence of a depth scale — the override must not invent wire fields.
	c := &client{model: "mimo-v2"}
	if got := c.buildRequest(provider.Request{EffortOverride: "low"}).ReasoningEffort; got != "" {
		t.Fatalf("reasoning_effort = %q, want omitted", got)
	}

	disabled := newTestClient(t, "deepseek-v4", map[string]any{"reasoning_protocol": "deepseek", "thinking": "disabled"})
	if got := disabled.buildRequest(provider.Request{EffortOverride: "high"}).ReasoningEffort; got != "" {
		t.Fatalf("thinking-disabled provider must ignore overrides, got %q", got)
	}
}
