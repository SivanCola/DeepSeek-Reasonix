package config

import "testing"

func TestValidateRuntimeContractConfigRejectsIllegal(t *testing.T) {
	cfg := Default()
	cfg.Tools.CapabilitySurface = "nope"
	if err := validateRuntimeContractConfig(cfg); err == nil {
		t.Fatal("expected error for bad capability_surface")
	}
	cfg = Default()
	cfg.Tools.EditProtocol = "hashline"
	cfg.Tools.CapabilitySurface = "legacy"
	if err := validateRuntimeContractConfig(cfg); err == nil {
		t.Fatal("hashline requires stable")
	}
	cfg = Default()
	cfg.Tools.CapabilitySurface = "stable"
	cfg.Tools.EditProtocol = "hashline"
	cfg.Agent.PromptLayout = "session_context"
	if err := validateRuntimeContractConfig(cfg); err != nil {
		t.Fatal(err)
	}
	surface, edit, layout, err := cfg.NewSessionRuntimeContract()
	if err != nil {
		t.Fatal(err)
	}
	if surface != "capability-v2" || edit != "hashline-v1" || layout != "session-context-v2" {
		t.Fatalf("got %s %s %s", surface, edit, layout)
	}
}

func TestNewSessionRuntimeContractEmptyIsLegacy(t *testing.T) {
	cfg := Default()
	surface, edit, layout, err := cfg.NewSessionRuntimeContract()
	if err != nil {
		t.Fatal(err)
	}
	if surface != "legacy-v1" || edit != "classic-v1" || layout != "system-v1" {
		t.Fatalf("empty defaults should be legacy triple, got %s/%s/%s", surface, edit, layout)
	}
}
