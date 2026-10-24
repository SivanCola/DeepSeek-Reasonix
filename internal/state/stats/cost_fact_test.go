package stats

import (
	"testing"

	"reasonix/internal/base/testenv"
)

// A row that counted tokens and carries its amount is complete. The "no cost
// fact" rule must not turn a normally quoted call into a floor.
func TestAQuotedCallIsNotIncomplete(t *testing.T) {
	dir := testenv.TempDir(t)
	writeDay(t, dir, "2026-08-10",
		map[string]any{"ts": "2026-08-10T01:00:00Z", "model": "deepseek/x", "source": "cli",
			"total": 100, "requests": 1, "cost_amount": "2", "cost_currency": "USD",
			"cost_complete": true, "billing_mode": "payg"},
	)
	got := queryAll(t, dir)
	if got.Tokens != 100 || got.Requests != 1 {
		t.Fatalf("tokens/requests = %d/%d, want the call counted", got.Tokens, got.Requests)
	}
	if len(got.Cost) != 1 || got.Cost[0].Amount != "2" {
		t.Fatalf("cost = %+v, want USD 2", got.Cost)
	}
	if got.CostIncomplete {
		t.Fatalf("costIncomplete = true for a row the file says cost_complete=true; cost = %+v", got.Cost)
	}
}

// An explicit "could not price it" quote keeps setting the flag, even when the
// row still carries an amount.
func TestAnExplicitlyIncompleteQuoteSetsTheFlag(t *testing.T) {
	dir := testenv.TempDir(t)
	writeDay(t, dir, "2026-08-10",
		map[string]any{"ts": "2026-08-10T01:00:00Z", "model": "deepseek/x", "source": "cli",
			"total": 100, "requests": 1, "cost_amount": "0", "cost_complete": false,
			"incomplete_reason": "no_price"},
	)
	if got := queryAll(t, dir); !got.CostIncomplete {
		t.Fatalf("costIncomplete = false for a row whose quote says cost_complete=false; cost = %+v", got.Cost)
	}
}

// A row written before the cost fields existed has neither cost_complete nor
// an amount. It counts its tokens and its money is unknown, not zero.
func TestALegacyRowWithNoCostFactSetsTheFlag(t *testing.T) {
	dir := testenv.TempDir(t)
	writeDay(t, dir, "2026-08-10",
		map[string]any{"ts": "2026-08-10T01:00:00Z", "model": "deepseek/x", "source": "cli",
			"total": 100, "requests": 1},
	)
	got := queryAll(t, dir)
	if got.Tokens != 100 || got.Requests != 1 {
		t.Fatalf("tokens/requests = %d/%d, want the call counted", got.Tokens, got.Requests)
	}
	if len(got.Cost) != 0 {
		t.Fatalf("cost = %+v, want no amount invented for the legacy row", got.Cost)
	}
	if !got.CostIncomplete {
		t.Fatal("costIncomplete = false: the file counted a 100-token call and recorded no money, so the zero must read as unknown")
	}
}

// A turn marker records a completed turn, not a provider call: it must never
// be read as an unpriced call.
func TestATurnMarkerIsNotAnUnpricedCall(t *testing.T) {
	dir := testenv.TempDir(t)
	writeDay(t, dir, "2026-08-10",
		map[string]any{"ts": "2026-08-10T01:00:00Z", "turn": true, "source": "cli"},
		map[string]any{"ts": "2026-08-10T01:01:00Z", "model": "deepseek/x", "source": "cli",
			"total": 100, "requests": 1, "cost_amount": "2", "cost_currency": "USD", "cost_complete": true},
	)
	got := queryAll(t, dir)
	if got.Turns != 1 {
		t.Fatalf("turns = %d, want the marker counted", got.Turns)
	}
	if got.CostIncomplete {
		t.Fatalf("costIncomplete = true: a turn marker carries no tokens and must not be read as an unpriced call; cost = %+v", got.Cost)
	}
}

// The catalog append path applies the same rule as the file read.
func TestUsageEntryCarriesTheNoCostFactRule(t *testing.T) {
	complete := true
	cases := []struct {
		name string
		rec  record
		want bool
	}{
		{"quoted", record{Total: 100, CostAmount: "2", CostCurrency: "USD", CostComplete: &complete}, false},
		{"explicitly incomplete", record{Total: 100, CostComplete: new(bool)}, true},
		{"no cost fact", record{Total: 100}, true},
		{"turn marker", record{Turn: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := usageEntry("2026-08-10", tc.rec).CostIncomplete; got != tc.want {
				t.Fatalf("usageEntry(%+v).CostIncomplete = %v, want %v", tc.rec, got, tc.want)
			}
		})
	}
}
