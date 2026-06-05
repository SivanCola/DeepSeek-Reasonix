package config

import (
	"os"
	"strings"
	"testing"
)

// testCfg returns a config with two providers where the first has a key set.
func testCfg() *Config {
	os.Setenv("TEST_KEY", "sk-test")
	os.Unsetenv("NO_SUCH_KEY")
	t := Default()
	// Replace with test-only providers so we control configuration.
	t.Providers = []ProviderEntry{
		{Name: "prov-a", Kind: "openai", BaseURL: "https://a.example.com", Model: "model-a1", Models: []string{"model-a1", "model-a2"}, APIKeyEnv: "TEST_KEY"},
		{Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com", Model: "model-b1", Models: []string{"model-b1", "model-b2"}, APIKeyEnv: "TEST_KEY"},
		{Name: "prov-nokey", Kind: "openai", BaseURL: "https://nk.example.com", Model: "model-nk", APIKeyEnv: "NO_SUCH_KEY"},
	}
	return t
}

func TestResolveModelWithFallback_DirectHit(t *testing.T) {
	c := testCfg()
	resolved, fallback, ok := c.ResolveModelWithFallback("prov-a/model-a1")
	if !ok || fallback {
		t.Fatalf("direct hit: ok=%v fallback=%v resolved=%q", ok, fallback, resolved)
	}
	if resolved != "prov-a/model-a1" {
		t.Errorf("resolved = %q, want prov-a/model-a1", resolved)
	}
}

func TestResolveModelWithFallback_ProviderName(t *testing.T) {
	c := testCfg()
	resolved, fallback, ok := c.ResolveModelWithFallback("prov-b")
	if !ok || fallback {
		t.Fatalf("provider name: ok=%v fallback=%v", ok, fallback)
	}
	if resolved != "prov-b/model-b1" {
		t.Errorf("resolved = %q, want prov-b/model-b1", resolved)
	}
}

func TestResolveModelWithFallback_BareModel(t *testing.T) {
	c := testCfg()
	resolved, fallback, ok := c.ResolveModelWithFallback("model-a2")
	if !ok || fallback {
		t.Fatalf("bare model: ok=%v fallback=%v", ok, fallback)
	}
	if resolved != "prov-a/model-a2" {
		t.Errorf("resolved = %q, want prov-a/model-a2", resolved)
	}
}

func TestResolveModelWithFallback_EmptyString(t *testing.T) {
	c := testCfg()
	resolved, fallback, ok := c.ResolveModelWithFallback("")
	if !ok || !fallback {
		t.Fatalf("empty: ok=%v fallback=%v", ok, fallback)
	}
	if !strings.HasPrefix(resolved, "prov-a/") {
		t.Errorf("fallback should pick first configured; got %q", resolved)
	}
}

func TestResolveModelWithFallback_StaleRef(t *testing.T) {
	c := testCfg()
	resolved, fallback, ok := c.ResolveModelWithFallback("deleted-prov/model")
	if !ok || !fallback {
		t.Fatalf("stale ref: ok=%v fallback=%v", ok, fallback)
	}
	if !strings.HasPrefix(resolved, "prov-a/") {
		t.Errorf("fallback should pick first configured; got %q", resolved)
	}
}

func TestResolveModelWithFallback_StaleDefaultModel(t *testing.T) {
	c := testCfg()
	c.DefaultModel = "deleted-prov"
	resolved, fallback, ok := c.ResolveModelWithFallback(c.DefaultModel)
	if !ok || !fallback {
		t.Fatalf("stale default: ok=%v fallback=%v", ok, fallback)
	}
	if !strings.HasPrefix(resolved, "prov-a/") {
		t.Errorf("fallback should pick first configured; got %q", resolved)
	}
}

func TestResolveModelWithFallback_NoConfiguredProviders(t *testing.T) {
	c := testCfg()
	c.Providers = []ProviderEntry{
		{Name: "nk", Kind: "openai", BaseURL: "https://nk.example.com", Model: "m", APIKeyEnv: "NO_SUCH_KEY"},
	}
	_, fallback, ok := c.ResolveModelWithFallback("nk/m")
	if ok {
		t.Error("expected no fallback when no provider is configured")
	}
	_ = fallback
}

func TestResolveModelWithFallback_SkipsUnconfigured(t *testing.T) {
	c := testCfg()
	// Only the unconfigured provider has the model being searched.
	c.Providers = []ProviderEntry{
		{Name: "nk", Kind: "openai", BaseURL: "https://nk.example.com", Model: "m", APIKeyEnv: "NO_SUCH_KEY"},
		{Name: "good", Kind: "openai", BaseURL: "https://g.example.com", Model: "g", APIKeyEnv: "TEST_KEY"},
	}
	resolved, fallback, ok := c.ResolveModelWithFallback("nk/m")
	if !ok || !fallback {
		t.Fatalf("should fallback to configured provider: ok=%v fallback=%v", ok, fallback)
	}
	if resolved != "good/g" {
		t.Errorf("resolved = %q, want good/g", resolved)
	}
}

func TestModelRefsProvider_BareName(t *testing.T) {
	if !ModelRefsProvider("deepseek-flash", "deepseek-flash") {
		t.Error("bare name should match")
	}
	if ModelRefsProvider("other", "deepseek-flash") {
		t.Error("different bare names should not match")
	}
}

func TestModelRefsProvider_ProviderModel(t *testing.T) {
	if !ModelRefsProvider("deepseek-flash/deepseek-v4-flash", "deepseek-flash") {
		t.Error("provider/model ref should match provider name")
	}
	if ModelRefsProvider("other/model", "deepseek-flash") {
		t.Error("different provider in ref should not match")
	}
}

func TestModelRefsProvider_Empty(t *testing.T) {
	if ModelRefsProvider("", "any") {
		t.Error("empty ref should not match any provider")
	}
}

func TestRemoveProvider_MigratesDefaultModel_BareName(t *testing.T) {
	c := testCfg()
	c.DefaultModel = "prov-a"
	if err := c.RemoveProvider("prov-a"); err != nil {
		t.Fatalf("RemoveProvider: %v", err)
	}
	if len(c.Providers) != 2 {
		t.Fatalf("expected 2 providers left, got %d", len(c.Providers))
	}
	if c.DefaultModel != "prov-b" {
		t.Errorf("default_model should migrate to prov-b, got %q", c.DefaultModel)
	}
}

func TestRemoveProvider_MigratesDefaultModel_ProviderModelRef(t *testing.T) {
	c := testCfg()
	c.DefaultModel = "prov-a/model-a2"
	if err := c.RemoveProvider("prov-a"); err != nil {
		t.Fatalf("RemoveProvider: %v", err)
	}
	if c.DefaultModel != "prov-b" {
		t.Errorf("default_model should migrate to prov-b, got %q", c.DefaultModel)
	}
}

func TestRemoveProvider_MigratesPlannerModel(t *testing.T) {
	c := testCfg()
	c.Agent.PlannerModel = "prov-a"
	if err := c.RemoveProvider("prov-a"); err != nil {
		t.Fatalf("RemoveProvider: %v", err)
	}
	if c.Agent.PlannerModel != "prov-b" {
		t.Errorf("planner_model should migrate to prov-b, got %q", c.Agent.PlannerModel)
	}
}

func TestRemoveProvider_MigratesPlannerModel_ProviderModelRef(t *testing.T) {
	c := testCfg()
	c.Agent.PlannerModel = "prov-a/model-a2"
	if err := c.RemoveProvider("prov-a"); err != nil {
		t.Fatalf("RemoveProvider: %v", err)
	}
	if c.Agent.PlannerModel != "prov-b" {
		t.Errorf("planner_model should migrate to prov-b, got %q", c.Agent.PlannerModel)
	}
}

func TestRemoveProvider_MigratesBoth(t *testing.T) {
	c := testCfg()
	c.DefaultModel = "prov-a"
	c.Agent.PlannerModel = "prov-a/model-a2"
	if err := c.RemoveProvider("prov-a"); err != nil {
		t.Fatalf("RemoveProvider: %v", err)
	}
	if c.DefaultModel != "prov-b" {
		t.Errorf("default_model = %q, want prov-b", c.DefaultModel)
	}
	if c.Agent.PlannerModel != "prov-b" {
		t.Errorf("planner_model = %q, want prov-b", c.Agent.PlannerModel)
	}
}

func TestRemoveProvider_NoFallbackAvailable(t *testing.T) {
	c := testCfg()
	c.DefaultModel = "prov-a"
	// Make prov-b unconfigured.
	c.Providers[1].APIKeyEnv = "NO_SUCH_KEY"
	// prov-nokey is already unconfigured.
	if err := c.RemoveProvider("prov-a"); err == nil {
		t.Error("expected error when no fallback exists")
	}
}

func TestRemoveProvider_PlannerClearedWhenNoFallback(t *testing.T) {
	c := testCfg()
	c.Agent.PlannerModel = "prov-a"
	// Make prov-b unconfigured.
	c.Providers[1].APIKeyEnv = "NO_SUCH_KEY"
	// Only default_model blocks when no fallback; planner can just be cleared.
	// But since planner migration also looks for fallback and won't find one,
	// it should still be cleared rather than left dangling.
	err := c.RemoveProvider("prov-a")
	// RemoveProvider blocks because planner has no fallback and defaults to clearing.
	// Actually, with a non-default ref it clears planner and succeeds.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Agent.PlannerModel != "" {
		t.Errorf("planner_model should be cleared, got %q", c.Agent.PlannerModel)
	}
}

func TestRemoveProvider_UnknownName(t *testing.T) {
	c := testCfg()
	if err := c.RemoveProvider("nope"); err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestRemoveProvider_DefaultAndPlannerBothRefProviderModel(t *testing.T) {
	c := testCfg()
	c.DefaultModel = "prov-a/model-a1"
	c.Agent.PlannerModel = "prov-a/model-a2"
	if err := c.RemoveProvider("prov-a"); err != nil {
		t.Fatalf("RemoveProvider: %v", err)
	}
	if c.DefaultModel != "prov-b" {
		t.Errorf("default_model = %q, want prov-b", c.DefaultModel)
	}
	if c.Agent.PlannerModel != "prov-b" {
		t.Errorf("planner_model = %q, want prov-b", c.Agent.PlannerModel)
	}
}
