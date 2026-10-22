package config

import (
	"slices"
	"strings"
	"testing"
)

func TestOwedChecksAreWaivedUnlessTheUserOrTheDeliveryPresetHoldsThem(t *testing.T) {
	for _, tc := range []struct {
		value    string
		delivery bool
		want     bool
	}{
		{"", false, true}, {"", true, false},
		{"on", false, false}, {"on", true, false},
		{"off", false, true}, {"off", true, true}, {"Off", false, true}, {" off ", true, true},
		{"false", false, false}, {"nonsense", false, false}, {"nonsense", true, false},
	} {
		cfg := Default()
		cfg.Agent.HostVerification = tc.value
		if got := cfg.WaivesOwedChecks(tc.delivery); got != tc.want {
			t.Errorf("host_verification = %q, delivery %v -> waived %v, want %v", tc.value, tc.delivery, got, tc.want)
		}
	}
}

func TestUserFileTurnsHostVerificationOff(t *testing.T) {
	cfg, _ := loadScoped(t, "[agent]\nhost_verification = \"off\"\n", "")
	if !cfg.WaivesOwedChecks(false) {
		t.Fatalf("a user file's off was not honoured: %q", cfg.Agent.HostVerification)
	}
}

func TestProjectCannotChangeHostVerificationEitherWay(t *testing.T) {
	for _, value := range []string{"on", "off"} {
		cfg, _ := loadScoped(t, "", "[agent]\nhost_verification = \""+value+"\"\n")
		if !cfg.WaivesOwedChecks(false) || cfg.WaivesOwedChecks(true) {
			t.Fatalf("a cloned repo's reasonix.toml (%q) moved what the host asks of a turn: waived %v, under delivery %v", value, cfg.WaivesOwedChecks(false), cfg.WaivesOwedChecks(true))
		}
		if !slices.Contains(ignoredKeys(cfg), "agent.host_verification") {
			t.Fatalf("ignored = %v, want agent.host_verification reported", ignoredKeys(cfg))
		}
	}
}

func TestProjectCannotTurnBackOnWhatTheUserTurnedOff(t *testing.T) {
	cfg, _ := loadScoped(t, "[agent]\nhost_verification = \"off\"\n", "[agent]\nhost_verification = \"on\"\n")
	if !cfg.WaivesOwedChecks(false) {
		t.Fatal("the project file overrode the user's own host_verification")
	}
}

func TestHostVerificationOffSurvivesASave(t *testing.T) {
	cfg := Default()
	cfg.Agent.HostVerification = "off"
	if out := RenderTOMLForScope(cfg, RenderScopeUser); !strings.Contains(out, "host_verification = \"off\"") {
		t.Fatalf("rendered config lost the choice:\n%s", out)
	}
	if out := RenderTOMLForScope(Default(), RenderScopeUser); strings.Contains(out, "\nhost_verification = ") {
		t.Fatalf("the default was written as an active key:\n%s", out)
	}
}
