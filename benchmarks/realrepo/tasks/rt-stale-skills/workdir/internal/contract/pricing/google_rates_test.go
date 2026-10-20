package pricing

import "testing"

// https://ai.google.dev/gemini-api/docs/pricing (read 2026-10-17): per 1M tokens,
// paid tier. gemini-3.8-flash is 0.75 in / 3.75 out / 0.075 cached through
// 2026-12-31 and 1.50 / 7.50 / 0.15 from 2027-01-01.
func TestAGeminiRateStepIsDatedNotEdited(t *testing.T) {
	was := today
	defer func() { today = was }()
	for _, tc := range []struct {
		day                  string
		cache, input, output float64
	}{
		{"2026-10-17", 0.075, 0.75, 3.75},
		{"2026-12-31", 0.075, 0.75, 3.75},
		{"2027-01-01", 0.15, 1.50, 7.50},
		{"2028-03-01", 0.15, 1.50, 7.50},
	} {
		today = func() string { return tc.day }
		got := CurrentRate("google", "gemini-3.8-flash", "USD")
		if got == nil || got.CacheHit != tc.cache || got.Input != tc.input || got.Output != tc.output {
			t.Errorf("%s: rate = %+v, want %v/%v/%v", tc.day, got, tc.cache, tc.input, tc.output)
		}
	}
}

func TestTheRateInForceIsNotListedAsSuperseded(t *testing.T) {
	was := today
	defer func() { today = was }()
	today = func() string { return "2026-10-17" }
	if sup := SupersededRates("google", "gemini-3.8-flash", "USD"); len(sup) != 0 {
		t.Fatalf("before the step nothing is superseded, got %+v", sup)
	}
	today = func() string { return "2027-02-01" }
	sup := SupersededRates("google", "gemini-3.8-flash", "USD")
	if len(sup) != 1 || sup[0].Input != 0.75 {
		t.Fatalf("after the step the intro rate is superseded, got %+v", sup)
	}
	if !IsOfficialCard("google", "gemini-3.8-flash", RateCard{CacheHit: 0.075, Input: 0.75, Output: 3.75, Currency: "USD"}) {
		t.Fatal("a stored intro card must still read as ours after the step")
	}
}

func TestGoogleModelsAreBilledAtGoogleOnlyOnGoogleHosts(t *testing.T) {
	if OfficialProviderForEndpoint("https://generativelanguage.googleapis.com/v1beta") != "google" {
		t.Fatal("Google's own host does not read as Google")
	}
	if OfficialProviderForEndpoint("https://relay.example.com/v1beta") != "" {
		t.Fatal("a relay was billed at Google's list price")
	}
}

func TestEveryGoogleRowNamesItsSource(t *testing.T) {
	n := 0
	for _, e := range OfficialCatalog() {
		if e.Provider != "google" {
			continue
		}
		n++
		if e.DocURL != DocGeminiPricing || e.CheckedOn != googleCheckedOn || e.Input <= 0 || e.Output <= e.Input {
			t.Errorf("row %+v", e)
		}
	}
	if n != 4 {
		t.Fatalf("%d Google rows, want 4 (gemini-4-argon shows no price)", n)
	}
}
