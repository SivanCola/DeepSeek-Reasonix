package stats

// A zero-amount quote is priced money: production writes cost_amount "0" with
// a currency when a call costs nothing, and both read paths must report that
// zero and its estimate flag or 今日 and 30 天 disagree about one history.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"reasonix/internal/base/testenv"
	"reasonix/internal/contract/pricing"
	"reasonix/internal/state/usagecatalog"
)

// queryLiveProjection builds the projection the production way: each record is
// appended to the day file and enqueued as a receipt, so projectRecord (not a
// full-day rederive) supplies the row's money.
func queryLiveProjection(t *testing.T, dir string, filter SourceFilter, lines ...map[string]any) RangeStats {
	t.Helper()
	ctx := context.Background()
	catalog, err := usagecatalog.Open(ctx, filepath.Join(testenv.TempDir(t), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(ctx) })
	manager := &usageManager{}
	manager.catalog.Store(catalog)
	w := NewWriter(dir)
	w.usage = manager
	for _, line := range lines {
		b, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		var r record
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatal(err)
		}
		if err := w.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := catalog.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if !catalog.Ready(ctx, dir, daysInRange(filter.From, filter.To)) {
		t.Fatal("the live projection is not ready, so this would read the JSONL instead")
	}
	got, err := w.Query(filter)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// queryProjection reads through a fully reconciled catalog and fails if the
// catalog is not ready: a silent fallback to JSONL would test nothing. A full
// reconcile derives the money with rederiveDays, the other production path.
func queryProjection(t *testing.T, dir string, filter SourceFilter) RangeStats {
	t.Helper()
	ctx := context.Background()
	catalog, err := usagecatalog.Open(ctx, filepath.Join(testenv.TempDir(t), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(ctx) })
	if err := catalog.ReconcileDir(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if !catalog.Ready(ctx, dir, daysInRange(filter.From, filter.To)) {
		t.Fatal("the projection is not ready, so this would read the JSONL instead")
	}
	manager := &usageManager{}
	manager.catalog.Store(catalog)
	w := NewWriter(dir)
	w.usage = manager
	got, err := w.Query(filter)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func rangeFromDays(t *testing.T, from, to string) SourceFilter {
	t.Helper()
	f, err := time.Parse(dayLayout, from)
	if err != nil {
		t.Fatal(err)
	}
	u, err := time.Parse(dayLayout, to)
	if err != nil {
		t.Fatal(err)
	}
	return SourceFilter{From: f, To: u}
}

func dayCost(t *testing.T, r RangeStats, day string) DailyTokens {
	t.Helper()
	for _, d := range r.Daily {
		if d.Day == day {
			return d
		}
	}
	t.Fatalf("no %s row in %+v", day, r.Daily)
	return DailyTokens{}
}

// Fixed two-day history with one zero-amount quoted row: the exact read, the
// incrementally built projection and the rebuilt projection must report the
// same money, including the zero, and the same estimate fact.
func TestZeroAmountQuoteThroughBothReadPaths(t *testing.T) {
	type fixture struct {
		day   string
		lines []map[string]any
	}
	days := []fixture{
		{day: "2026-08-01", lines: []map[string]any{
			{"ts": "2026-08-01T02:00:00Z", "model": "deepseek/deepseek-chat", "source": "desktop",
				"prompt": 80, "completion": 20, "total": 100, "requests": 1,
				"cost_amount": "1.50", "cost_currency": "CNY", "valuation_cny": "1.50",
				"cost_complete": true, "cost_estimated": true, "billing_mode": "payg"},
		}},
		{day: "2026-09-01", lines: []map[string]any{
			{"ts": "2026-09-01T02:00:00Z", "model": "deepseek/deepseek-chat", "source": "desktop",
				"prompt": 80, "completion": 20, "total": 100, "requests": 1,
				"cost_amount": "1.50", "cost_currency": "CNY", "valuation_cny": "1.50",
				"cost_complete": true, "cost_estimated": true, "billing_mode": "payg"},
			// The quote priced this call at zero: amount "0" with a currency.
			{"ts": "2026-09-01T03:00:00Z", "model": "relay/free", "source": "desktop",
				"prompt": 40, "completion": 10, "total": 50, "requests": 1,
				"cost_amount": "0", "cost_currency": "USD", "valuation_usd": "0",
				"cost_complete": true, "cost_estimated": true, "billing_mode": "payg"},
			// Written before the cost fields existed: no currency, no money.
			{"ts": "2026-09-01T05:00:00Z", "model": "deepseek/deepseek-chat", "source": "cli",
				"prompt": 15, "completion": 5, "total": 20, "requests": 1},
			{"ts": "2026-09-01T06:00:00Z", "model": "relay/free", "source": "cli", "requests": 1},
			{"ts": "2026-09-01T07:00:00Z", "source": "desktop", "turn": true},
		}},
	}
	var lines []map[string]any
	for _, d := range days {
		lines = append(lines, d.lines...)
	}
	dir := testenv.TempDir(t)
	filter := rangeFromDays(t, "2026-08-01", "2026-09-30")
	fromLive := queryLiveProjection(t, dir, filter, lines...)
	fromFiles, err := NewWriter(dir).Query(filter)
	if err != nil {
		t.Fatal(err)
	}
	fromProjection := queryProjection(t, dir, filter)

	wantTotal := []pricing.Money{{Amount: "3", Currency: "CNY"}, {Amount: "0", Currency: "USD"}}
	wantDay := []pricing.Money{{Amount: "1.5", Currency: "CNY"}, {Amount: "0", Currency: "USD"}}
	paths := map[string]RangeStats{"JSONL": fromFiles, "live projection": fromLive, "rebuilt projection": fromProjection}
	for name, got := range paths {
		if !reflect.DeepEqual(got.Cost, wantTotal) {
			t.Fatalf("%s cost = %+v, want %+v", name, got.Cost, wantTotal)
		}
		if gotDay := dayCost(t, got, "2026-09-01").Cost; !reflect.DeepEqual(gotDay, wantDay) {
			t.Fatalf("%s 2026-09-01 cost = %+v, want %+v", name, gotDay, wantDay)
		}
		if !got.CostEstimated {
			t.Fatalf("%s costEstimated = false, want true", name)
		}
		if !reflect.DeepEqual(got, fromFiles) {
			t.Errorf("%s disagrees with the exact read\nfiles: %+v\n%s: %+v", name, fromFiles, name, got)
		}
	}
}

// A day whose only quote is zero still folded in an estimate-priced quote, so
// the "includes estimates" badge must survive on both read paths.
func TestZeroAmountOnlyDayKeepsEstimateFlag(t *testing.T) {
	dir := testenv.TempDir(t)
	lines := []map[string]any{
		{"ts": "2026-09-10T02:00:00Z", "model": "local/free", "source": "desktop",
			"prompt": 50, "total": 50, "requests": 1,
			"cost_amount": "0", "cost_currency": "USD", "valuation_usd": "0",
			"cost_complete": true, "cost_estimated": true, "billing_mode": "payg"},
	}
	filter := rangeFromDays(t, "2026-09-09", "2026-09-11")
	fromLive := queryLiveProjection(t, dir, filter, lines...)
	fromFiles, err := NewWriter(dir).Query(filter)
	if err != nil {
		t.Fatal(err)
	}
	fromProjection := queryProjection(t, dir, filter)

	want := []pricing.Money{{Amount: "0", Currency: "USD"}}
	paths := map[string]RangeStats{"JSONL": fromFiles, "live projection": fromLive, "rebuilt projection": fromProjection}
	for name, got := range paths {
		if !got.CostEstimated {
			t.Errorf("%s: costEstimated = false for a day whose only quote is an estimate", name)
		}
		if !reflect.DeepEqual(got.Cost, want) {
			t.Errorf("%s: cost = %+v, want %+v", name, got.Cost, want)
		}
		if day := dayCost(t, got, "2026-09-10"); !reflect.DeepEqual(day.Cost, want) || day.Total != 50 {
			t.Errorf("%s: day = %+v, want 50 tokens and %+v", name, day, want)
		}
		if !reflect.DeepEqual(got, fromFiles) {
			t.Errorf("%s disagrees with the exact read\nfiles: %+v\n%s: %+v", name, fromFiles, name, got)
		}
	}
}

// A row with no currency carries no price: it stays out of both money lists,
// and the costIncomplete fact it contributes is the same on both paths.
func TestRowWithoutCurrencyStaysOutOfMoney(t *testing.T) {
	dir := testenv.TempDir(t)
	writeDay(t, dir, "2026-09-12",
		map[string]any{"ts": "2026-09-12T02:00:00Z", "model": "deepseek/x", "source": "cli",
			"total": 100, "requests": 1, "cost_amount": "1.50", "cost_currency": "CNY"},
		// No amount and no currency: its money is unknown, not zero.
		map[string]any{"ts": "2026-09-12T03:00:00Z", "model": "relay/unknown", "source": "cli",
			"total": 20, "requests": 1},
	)
	filter := rangeFromDays(t, "2026-09-11", "2026-09-13")
	fromFiles, err := NewWriter(dir).Query(filter)
	if err != nil {
		t.Fatal(err)
	}
	fromProjection := queryProjection(t, dir, filter)

	want := []pricing.Money{{Amount: "1.5", Currency: "CNY"}}
	if !reflect.DeepEqual(fromFiles.Cost, want) || !reflect.DeepEqual(fromProjection.Cost, want) {
		t.Fatalf("a currency-less row entered the money: JSONL %+v projection %+v, want %+v",
			fromFiles.Cost, fromProjection.Cost, want)
	}
	if !fromFiles.CostIncomplete || !fromProjection.CostIncomplete {
		t.Errorf("costIncomplete JSONL=%v projection=%v, want both true", fromFiles.CostIncomplete, fromProjection.CostIncomplete)
	}
	if !reflect.DeepEqual(fromFiles, fromProjection) {
		t.Errorf("the two read paths disagree\nfiles:      %+v\nprojection: %+v", fromFiles, fromProjection)
	}
}

// Non-zero amounts project exactly as they did before: both paths keep the
// per-currency split and the per-day breakdown.
func TestNonZeroAmountProjectionUnchanged(t *testing.T) {
	dir := testenv.TempDir(t)
	writeDay(t, dir, "2026-09-14",
		map[string]any{"ts": "2026-09-14T02:00:00Z", "model": "deepseek/x", "source": "cli", "total": 100, "requests": 1,
			"cost_amount": "1.50", "cost_currency": "CNY"},
		map[string]any{"ts": "2026-09-14T03:00:00Z", "model": "relay/y", "source": "cli", "total": 50, "requests": 1,
			"cost_amount": "0.25", "cost_currency": "USD"},
	)
	writeDay(t, dir, "2026-09-15",
		map[string]any{"ts": "2026-09-15T02:00:00Z", "model": "deepseek/x", "source": "cli", "total": 100, "requests": 1,
			"cost_amount": "2.50", "cost_currency": "CNY"},
	)
	filter := rangeFromDays(t, "2026-09-14", "2026-09-15")
	fromFiles, err := NewWriter(dir).Query(filter)
	if err != nil {
		t.Fatal(err)
	}
	fromProjection := queryProjection(t, dir, filter)

	wantTotal := []pricing.Money{{Amount: "4", Currency: "CNY"}, {Amount: "0.25", Currency: "USD"}}
	if !reflect.DeepEqual(fromFiles.Cost, wantTotal) || !reflect.DeepEqual(fromProjection.Cost, wantTotal) {
		t.Fatalf("total cost JSONL %+v projection %+v, want %+v", fromFiles.Cost, fromProjection.Cost, wantTotal)
	}
	wantDay14 := []pricing.Money{{Amount: "1.5", Currency: "CNY"}, {Amount: "0.25", Currency: "USD"}}
	wantDay15 := []pricing.Money{{Amount: "2.5", Currency: "CNY"}}
	paths := map[string]RangeStats{"JSONL": fromFiles, "projection": fromProjection}
	for name, got := range paths {
		if day := dayCost(t, got, "2026-09-14").Cost; !reflect.DeepEqual(day, wantDay14) {
			t.Errorf("%s 2026-09-14 cost = %+v, want %+v", name, day, wantDay14)
		}
		if day := dayCost(t, got, "2026-09-15").Cost; !reflect.DeepEqual(day, wantDay15) {
			t.Errorf("%s 2026-09-15 cost = %+v, want %+v", name, day, wantDay15)
		}
		if !reflect.DeepEqual(got, fromFiles) {
			t.Errorf("%s disagrees with the exact read\nfiles: %+v\n%s: %+v", name, fromFiles, name, got)
		}
	}
}
