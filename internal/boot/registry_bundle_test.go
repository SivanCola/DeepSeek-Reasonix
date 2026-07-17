package boot

import (
	"context"
	"sort"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/tool"
)

func sortedToolNames(reg *tool.Registry) []string {
	if reg == nil {
		return nil
	}
	names := append([]string(nil), reg.Names()...)
	sort.Strings(names)
	return names
}

func hasTool(reg *tool.Registry, name string) bool {
	if reg == nil {
		return false
	}
	_, ok := reg.Get(name)
	return ok
}

func TestLegacyBootBundleAllowsResumeToCapability(t *testing.T) {
	// Pure legacy Full boot must keep provider schema free of v2 proxies,
	// while the RegistryBundle still carries complete capability surfaces.
	ctrl, err := Build(context.Background(), Options{
		Model:           "deepseek-flash",
		RequireKey:      false,
		Sink:            event.Discard,
		SessionDir:      t.TempDir(),
		WorkspaceRoot:   t.TempDir(),
		RuntimeContract: agent.LegacyDefaultRuntimeContract().Clone(),
	})
	if err != nil {
		t.Skipf("build unavailable: %v", err)
	}
	defer ctrl.Close()
	exec := ctrl.Executor()
	if exec == nil {
		t.Fatal("nil executor")
	}
	bundle := exec.RegistryBundle()
	if bundle == nil {
		t.Fatal("expected RegistryBundle on legacy boot")
	}
	// Active provider surface is pure legacy Full: no search_capabilities.
	if hasTool(bundle.Legacy, "search_capabilities") {
		t.Fatal("legacy registry must not include search_capabilities")
	}
	if hasTool(bundle.Legacy, "use_capability") {
		// Full profile (not Delivery) must not have use_capability on legacy.
		t.Fatal("legacy Full registry must not include use_capability")
	}
	// Capability surfaces must still expose both proxies for Resume.
	if !hasTool(bundle.CapabilityClassic, "use_capability") || !hasTool(bundle.CapabilityClassic, "search_capabilities") {
		t.Fatalf("capability-classic tools = %v, want use_capability+search_capabilities", sortedToolNames(bundle.CapabilityClassic))
	}
	if !hasTool(bundle.CapabilityHashline, "use_capability") || !hasTool(bundle.CapabilityHashline, "search_capabilities") {
		t.Fatalf("capability-hashline tools = %v, want proxies", sortedToolNames(bundle.CapabilityHashline))
	}
	if !hasTool(bundle.CapabilityHashline, "hashline_read") {
		t.Fatal("hashline surface missing hashline_read")
	}
	if hasTool(bundle.CapabilityHashline, "read_file") {
		t.Fatal("hashline surface must exclude classic read_file")
	}

	// Resume to capability-v2 classic: active tools gain proxies, not pollute Legacy.
	v2 := agent.DefaultRuntimeContract()
	if err := exec.ApplyRuntimeContract(v2, bundle); err != nil {
		t.Fatal(err)
	}
	if !hasTool(bundle.ProviderRegistry(v2), "use_capability") {
		t.Fatal("after resume to v2, provider must expose use_capability")
	}
	// Legacy snapshot must remain pure.
	if hasTool(bundle.Legacy, "use_capability") || hasTool(bundle.Legacy, "search_capabilities") {
		t.Fatal("ApplyRuntimeContract must not mutate the pure Legacy snapshot")
	}
}

func TestCapabilityBootBundleAllowsResumeToLegacy(t *testing.T) {
	v2 := agent.DefaultRuntimeContract()
	ctrl, err := Build(context.Background(), Options{
		Model:           "deepseek-flash",
		RequireKey:      false,
		Sink:            event.Discard,
		SessionDir:      t.TempDir(),
		WorkspaceRoot:   t.TempDir(),
		RuntimeContract: v2.Clone(),
	})
	if err != nil {
		t.Skipf("build unavailable: %v", err)
	}
	defer ctrl.Close()
	exec := ctrl.Executor()
	bundle := exec.RegistryBundle()
	if bundle == nil {
		t.Fatal("expected RegistryBundle")
	}
	// Active surface is capability classic.
	active := bundle.ProviderRegistry(exec.RuntimeContract())
	if !hasTool(active, "use_capability") || !hasTool(active, "search_capabilities") {
		t.Fatalf("v2 boot active tools = %v", sortedToolNames(active))
	}
	// Pure legacy snapshot must not carry v2 proxies (Full profile).
	if hasTool(bundle.Legacy, "use_capability") || hasTool(bundle.Legacy, "search_capabilities") {
		t.Fatalf("legacy snapshot polluted: %v", sortedToolNames(bundle.Legacy))
	}

	// Resume to legacy: switch to pure legacy schema.
	legacy := agent.LegacyDefaultRuntimeContract()
	if err := exec.ApplyRuntimeContract(legacy, bundle); err != nil {
		t.Fatal(err)
	}
	if got := exec.RuntimeContract(); !got.Equal(legacy) {
		t.Fatalf("contract = %+v", got)
	}
	// After apply, provider registry for legacy is the pure snapshot.
	leg := bundle.ProviderRegistry(legacy)
	if hasTool(leg, "use_capability") || hasTool(leg, "search_capabilities") {
		t.Fatalf("resumed legacy tools polluted: %v", sortedToolNames(leg))
	}
	if !hasTool(leg, "read_file") {
		t.Fatal("legacy surface should keep classic read_file")
	}
}
