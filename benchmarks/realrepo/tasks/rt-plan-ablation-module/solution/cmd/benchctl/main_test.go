package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func runOut(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := run(args, &out); err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return out.String()
}

func TestRateNamesTheCurrentCard(t *testing.T) {
	got := runOut(t, "rate", "-provider", "mimo", "-model", "mimo-v2.5")
	if !strings.Contains(got, "mimo/mimo-v2.5 CNY: cache-hit 0.02 input 1 output 2 per 1M") {
		t.Fatalf("rate output = %q", got)
	}
}

func TestRateRefusesAnUnpricedModel(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"rate", "-provider", "nobody", "-model", "x"}, &out); err == nil {
		t.Fatal("an unpriced model must fail")
	}
}

func TestCostChargesTheCurrentCard(t *testing.T) {
	got := runOut(t, "cost", "-provider", "mimo", "-model", "mimo-v2.5", "-miss", "1000000", "-completion", "1000000")
	if strings.TrimSpace(got) != "3 CNY" {
		t.Fatalf("cost output = %q", got)
	}
}

func TestArmNamesTheConfiguration(t *testing.T) {
	if got := strings.TrimSpace(runOut(t, "arm")); got != "full" {
		t.Fatalf("control arm = %q", got)
	}
	if got := strings.TrimSpace(runOut(t, "arm", "-ablate", "planner,evidence", "-fold-index", "half")); got != "no-evidence+no-planner+index-half" {
		t.Fatalf("arm = %q", got)
	}
}

func TestUnknownSubcommandIsAUsageError(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"nope"}, &out); !errors.Is(err, errUsage) {
		t.Fatalf("err = %v, want errUsage", err)
	}
}

func TestTariffReadsTheVendorsSchedule(t *testing.T) {
	got := runOut(t, "tariff", "-provider", "mimo", "-model", "mimo-v2.5")
	if !strings.Contains(got, "official card: true") || !strings.Contains(got, "one rate around the clock") {
		t.Fatalf("tariff output = %q", got)
	}
	peak := runOut(t, "tariff", "-currency", "CNY", "-at", "2026-10-12T02:00:00Z")
	if !strings.Contains(peak, "peak=true until 2026-10-12T04:00:00Z") {
		t.Fatalf("deepseek tariff at a weekday peak = %q", peak)
	}
}

func TestArmListShowsEveryModuleWithItsSummary(t *testing.T) {
	got := strings.TrimSpace(runOut(t, "arm", "-list"))
	if !strings.HasSuffix(got, "memory: no saved-fact memory store") || strings.Count(got, "\n") != 7 {
		t.Fatalf("arm -list = %q", got)
	}
}
