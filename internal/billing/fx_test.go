package billing

import (
	"strings"
	"testing"
	"time"
)

const sampleECB = `Date,USD,JPY,CNY
2026-03-06,1.0800,160.00,7.8000
2026-03-07,1.0850,161.00,7.8500
`

func TestParseECBAndCrossRate(t *testing.T) {
	table, err := ParseECBHistCSV(strings.NewReader(sampleECB))
	if err != nil {
		t.Fatal(err)
	}
	// 1 CNY → USD on 2026-03-07: USD/CNY = 1.085/7.85
	occurred, _ := time.Parse(time.RFC3339, "2026-03-07T15:00:00Z")
	one := NewAmountFromFloat(7.85) // 7.85 CNY ≈ 1.085 USD
	got, snap, ok := table.Convert(one, "CNY", "USD", occurred, 7)
	if !ok {
		t.Fatalf("convert failed: %+v", snap)
	}
	if snap.AsOf != "2026-03-07" {
		t.Fatalf("asOf = %q", snap.AsOf)
	}
	if got.Float64() < 1.08 || got.Float64() > 1.09 {
		t.Fatalf("converted = %v, want ~1.085", got.Float64())
	}
}

func TestWeekendFallback(t *testing.T) {
	table, err := ParseECBHistCSV(strings.NewReader(sampleECB))
	if err != nil {
		t.Fatal(err)
	}
	// Saturday after Friday 2026-03-07 — use Friday.
	sat, _ := time.Parse(time.RFC3339, "2026-03-08T12:00:00Z")
	_, snap, ok := table.Convert(NewAmountFromFloat(1), "USD", "CNY", sat, 7)
	if !ok {
		t.Fatal("weekend convert failed")
	}
	if snap.AsOf != "2026-03-07" {
		t.Fatalf("weekend asOf = %q, want 2026-03-07", snap.AsOf)
	}
}

func TestStaleCacheRejected(t *testing.T) {
	table, err := ParseECBHistCSV(strings.NewReader(sampleECB))
	if err != nil {
		t.Fatal(err)
	}
	// Occurred far in the future relative to table dates with maxAge 7.
	far, _ := time.Parse(time.RFC3339, "2026-04-01T00:00:00Z")
	_, snap, ok := table.Convert(NewAmountFromFloat(1), "USD", "CNY", far, 7)
	if ok {
		t.Fatalf("expected stale rejection, got snap=%+v", snap)
	}
	if snap == nil || !snap.Stale {
		t.Fatalf("expected stale snapshot, got %+v", snap)
	}
}

func TestUnknownCurrency(t *testing.T) {
	table, err := ParseECBHistCSV(strings.NewReader(sampleECB))
	if err != nil {
		t.Fatal(err)
	}
	_, _, ok := table.Convert(NewAmountFromFloat(1), "USD", "ZZZ", time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC), 7)
	if ok {
		t.Fatal("expected unknown currency failure")
	}
}

func TestFXCacheFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ecb.json"
	table, err := ParseECBHistCSV(strings.NewReader(sampleECB))
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveRateTableFile(path, table); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRateTableFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.IsEmpty() {
		t.Fatal("loaded empty")
	}
	if err := SaveRateTableFile(path, NewRateTable()); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRateTableFile(path); err == nil {
		t.Fatal("expected empty cache error")
	}
	if _, err := LoadRateTableFile(path + ".missing"); err == nil {
		t.Fatal("expected missing file error")
	}
}

func TestInitGlobalFXReusesCacheForControllerRebuilds(t *testing.T) {
	stateHome := t.TempDir()
	table, err := ParseECBHistCSV(strings.NewReader(sampleECB))
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveRateTableFile(DefaultFXCachePath(stateHome), table); err != nil {
		t.Fatal(err)
	}

	globalFXMu.Lock()
	previous := globalFX
	globalFX = nil
	globalFXMu.Unlock()
	t.Cleanup(func() {
		globalFXMu.Lock()
		globalFX = previous
		globalFXMu.Unlock()
	})

	first := InitGlobalFX(stateHome)
	second := InitGlobalFX(stateHome)
	if first != second {
		t.Fatal("same state home initialized multiple process-wide FX caches")
	}
}
