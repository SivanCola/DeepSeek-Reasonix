package stats

// The usage projection keys its files and records by absolute path. These tests
// pin what happens when the stats root moves (Settings → Storage move/adopt
// carries the directory and asks for a restart) and when one day file vanishes.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/base/testenv"
	"reasonix/internal/contract/config"
	"reasonix/internal/contract/event"
	"reasonix/internal/contract/pricing"
	"reasonix/internal/state/usagecatalog"
)

// auditIsolatedHome keeps every path the product would resolve inside the
// test's own temp tree: no test here reads or writes the developer's real home.
func auditIsolatedHome(t *testing.T) {
	t.Helper()
	t.Setenv("REASONIX_HOME", testenv.TempDir(t))
	t.Setenv("REASONIX_STATE_HOME", testenv.TempDir(t))
	t.Setenv("REASONIX_CACHE_HOME", testenv.TempDir(t))
}

func auditWriteDay(t *testing.T, dir, day string, lines ...map[string]any) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var buf []byte
	for _, line := range lines {
		encoded, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		buf = append(buf, encoded...)
		buf = append(buf, '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, day+".jsonl"), buf, 0o600); err != nil {
		t.Fatal(err)
	}
}

// auditFixture is a fixed two-day history: 270 tokens, 5 requests, CNY 3 of
// billed cost and one unpriced call. It is the audit's fixture, unchanged.
func auditFixture(t *testing.T) string {
	t.Helper()
	dir := testenv.TempDir(t)
	auditWriteDay(t, dir, "2026-08-01",
		map[string]any{"ts": "2026-08-01T02:00:00Z", "model": "deepseek/deepseek-chat", "source": "desktop",
			"prompt": 80, "completion": 20, "cache_hit": 10, "cache_miss": 70, "total": 100, "requests": 1,
			"cost_amount": "1.50", "cost_currency": "CNY", "valuation_cny": "1.50", "valuation_usd": "0.21",
			"cost_complete": true, "cost_estimated": true, "billing_mode": "payg"},
	)
	auditWriteDay(t, dir, "2026-09-01",
		map[string]any{"ts": "2026-09-01T02:00:00Z", "model": "deepseek/deepseek-chat", "source": "desktop",
			"prompt": 80, "completion": 20, "cache_hit": 10, "cache_miss": 70, "total": 100, "requests": 1,
			"cost_amount": "1.50", "cost_currency": "CNY", "valuation_cny": "1.50", "valuation_usd": "0.21",
			"cost_complete": true, "cost_estimated": true, "billing_mode": "payg"},
		map[string]any{"ts": "2026-09-01T03:00:00Z", "model": "relay/new-model", "source": "desktop",
			"prompt": 40, "completion": 10, "total": 50, "requests": 1,
			"cost_amount": "0", "cost_currency": "USD", "valuation_usd": "0",
			"cost_complete": false, "cost_estimated": true, "incomplete_reason": "missing_price_or_usage"},
		map[string]any{"ts": "2026-09-01T05:00:00Z", "model": "deepseek/deepseek-chat", "source": "cli",
			"prompt": 15, "completion": 5, "total": 20, "requests": 1},
		map[string]any{"ts": "2026-09-01T06:00:00Z", "model": "relay/new-model", "source": "cli", "requests": 1},
		map[string]any{"ts": "2026-09-01T07:00:00Z", "source": "desktop", "turn": true},
	)
	return dir
}

func auditFilter() SourceFilter {
	from, _ := time.Parse(dayLayout, "2026-08-01")
	to, _ := time.Parse(dayLayout, "2026-09-30")
	return SourceFilter{From: from, To: to}
}

// auditSummary is the payload as the page reads it: totals, both money lists,
// both cost flags, and the per-day money facts.
func auditSummary(t *testing.T, r RangeStats) string {
	t.Helper()
	type day struct {
		Day  string          `json:"day"`
		Cost []pricing.Money `json:"cost,omitempty"`
		Est  []pricing.Money `json:"estimateCost,omitempty"`
	}
	var days []day
	for _, d := range r.Daily {
		if d.Total == 0 && len(d.Cost) == 0 && d.Turns == 0 {
			continue
		}
		days = append(days, day{Day: d.Day, Cost: d.Cost, Est: d.EstimateCost})
	}
	out := map[string]any{
		"from": r.From, "to": r.To, "tokens": r.Tokens, "requests": r.Requests,
		"turns": r.Turns, "cache_hit": r.CacheHit, "cache_miss": r.CacheMiss,
		"cost": r.Cost, "estimateCost": r.EstimateCost,
		"costEstimated": r.CostEstimated, "costIncomplete": r.CostIncomplete,
		"active_days": r.ActiveDays, "daily": days,
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func auditQuery(t *testing.T, dir string, catalog *usagecatalog.Catalog) RangeStats {
	t.Helper()
	if catalog == nil {
		got, err := NewWriter(dir).Query(auditFilter())
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	manager := &usageManager{}
	manager.catalog.Store(catalog)
	w := NewWriter(dir)
	w.usage = manager
	got, err := w.Query(auditFilter())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func auditManagerCatalog(dir string) *usagecatalog.Catalog {
	manager := existingUsageManager(dir)
	if manager == nil {
		return nil
	}
	return manager.catalog.Load()
}

func auditWaitReady(t *testing.T, dir string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if catalog := auditManagerCatalog(dir); catalog != nil {
			if catalog.Ready(context.Background(), dir, daysInRange(auditFilter().From, auditFilter().To)) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("projection for %s never became ready", dir)
}

func auditedPayload(t *testing.T, r RangeStats) string {
	t.Helper()
	if r.Tokens != 270 || r.Requests != 5 || r.Turns != 1 {
		t.Fatalf("fixture totals = %d tokens / %d requests / %d turns, want 270 / 5 / 1", r.Tokens, r.Requests, r.Turns)
	}
	if len(r.Cost) != 2 || r.Cost[0].Currency != "CNY" || r.Cost[0].Amount != "3" || r.Cost[1].Currency != "USD" || r.Cost[1].Amount != "0" {
		t.Fatalf("fixture cost = %+v, want CNY 3 and the zero-amount USD quote", r.Cost)
	}
	return auditSummary(t, r)
}

// TestRelocatedStateRootDoesNotCountTwice is the audit's production-wiring
// test: boot's recorder starts the projection for the stats dir, Flush and
// CloseUsageCatalogs shut it down, the state root (with the stats directory) is
// renamed, the environment points at the new root, and the app is booted again.
// The same fixed history must read 270 / 5 / CNY 3 both times.
func TestRelocatedStateRootDoesNotCountTwice(t *testing.T) {
	auditIsolatedHome(t)
	rootA, rootB := testenv.TempDir(t), testenv.TempDir(t)
	t.Setenv("REASONIX_STATE_HOME", rootA)
	fixture := auditFixture(t)
	for _, name := range []string{"2026-08-01.jsonl", "2026-09-01.jsonl"} {
		src, err := os.ReadFile(filepath.Join(fixture, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(config.StatsDir(), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(config.StatsDir(), name), src, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()

	// Launch 1: boot installs stats.NewRecorder for the stats dir.
	if NewRecorder(event.Discard, config.StatsDir(), "desktop") == nil {
		t.Fatal("no recorder")
	}
	auditWaitReady(t, config.StatsDir())
	before := auditedPayload(t, auditQuery(t, config.StatsDir(), auditManagerCatalog(config.StatsDir())))
	t.Logf("before relocation: %s", before)

	// Shutdown, relocate the state root, relaunch.
	if err := Flush(ctx, config.StatsDir()); err != nil {
		t.Fatal(err)
	}
	if err := CloseUsageCatalogs(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(rootA, "stats"), filepath.Join(rootB, "stats")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REASONIX_STATE_HOME", rootB)

	// Launch 2: same boot wiring, new stats dir, same cache (the projection).
	if NewRecorder(event.Discard, config.StatsDir(), "desktop") == nil {
		t.Fatal("no recorder after relocation")
	}
	auditWaitReady(t, config.StatsDir())
	afterStats := auditQuery(t, config.StatsDir(), auditManagerCatalog(config.StatsDir()))
	after := auditedPayload(t, afterStats)
	t.Logf("after relocation:  %s", after)

	if before != after {
		t.Errorf("relocating the state root changed the projection\nbefore: %s\nafter:  %s", before, after)
	}
	// The exact JSONL truth (the same files in a directory no projection
	// indexes) must agree on what the move may not change.
	truth := auditQuery(t, fixture, nil)
	if afterStats.Tokens != truth.Tokens || afterStats.Requests != truth.Requests {
		t.Errorf("after relocating the state root the projection reports %d tokens / %d requests, JSONL says %d / %d",
			afterStats.Tokens, afterStats.Requests, truth.Tokens, truth.Requests)
	}
}

// TestVanishedDayFileIsNotServedAsComplete removes a day file after the
// projection indexed it. A missing day file is not a complete day: the
// projection must not keep serving its rows, and once the reconcile has
// forgotten the file the projection must read the same as JSONL.
func TestVanishedDayFileIsNotServedAsComplete(t *testing.T) {
	auditIsolatedHome(t)
	dir := auditFixture(t)
	ctx := context.Background()
	catalog, err := usagecatalog.Open(ctx, filepath.Join(testenv.TempDir(t), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	if err := catalog.ReconcileDir(ctx, dir); err != nil {
		t.Fatal(err)
	}
	days := daysInRange(auditFilter().From, auditFilter().To)
	if !catalog.Ready(ctx, dir, days) {
		t.Fatal("projection is not ready before the file vanishes")
	}
	if err := os.Remove(filepath.Join(dir, "2026-08-01.jsonl")); err != nil {
		t.Fatal(err)
	}

	if catalog.Ready(ctx, dir, days) {
		t.Error("Ready reports complete with the indexed day file gone: stale rows would be served")
	}
	fromFiles := auditQuery(t, dir, nil)
	fromProjection := auditQuery(t, dir, catalog)
	t.Logf("JSONL:      %s", auditSummary(t, fromFiles))
	t.Logf("projection: %s", auditSummary(t, fromProjection))
	if fromProjection.Tokens != fromFiles.Tokens || fromProjection.Requests != fromFiles.Requests {
		t.Errorf("projection reports %d tokens / %d requests with the day file gone, JSONL says %d / %d",
			fromProjection.Tokens, fromProjection.Requests, fromFiles.Tokens, fromFiles.Requests)
	}

	// Query asks the catalog to reconcile; the reconcile forgets the vanished
	// file instead of keeping its rows. Wait for it to converge.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !catalog.Ready(ctx, dir, days) {
		time.Sleep(10 * time.Millisecond)
	}
	if !catalog.Ready(ctx, dir, days) {
		t.Fatal("projection did not converge to ready after forgetting the vanished file")
	}
	converged := auditQuery(t, dir, catalog)
	if converged.Tokens != fromFiles.Tokens || converged.Requests != fromFiles.Requests || len(converged.Daily) != len(fromFiles.Daily) {
		t.Errorf("projection did not converge to JSONL after the reconcile\nJSONL:      %s\nprojection: %s",
			auditSummary(t, fromFiles), auditSummary(t, converged))
	}
}

// TestUnchangedRootDoesNotDoubleOnRelaunch is the control: two launches on the
// same root, with the reconcile running again, return the same numbers.
func TestUnchangedRootDoesNotDoubleOnRelaunch(t *testing.T) {
	auditIsolatedHome(t)
	fixture := auditFixture(t)
	for _, name := range []string{"2026-08-01.jsonl", "2026-09-01.jsonl"} {
		src, err := os.ReadFile(filepath.Join(fixture, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(config.StatsDir(), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(config.StatsDir(), name), src, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	launch := func() string {
		if NewRecorder(event.Discard, config.StatsDir(), "desktop") == nil {
			t.Fatal("no recorder")
		}
		auditWaitReady(t, config.StatsDir())
		payload := auditedPayload(t, auditQuery(t, config.StatsDir(), auditManagerCatalog(config.StatsDir())))
		if err := Flush(ctx, config.StatsDir()); err != nil {
			t.Fatal(err)
		}
		if err := CloseUsageCatalogs(ctx); err != nil {
			t.Fatal(err)
		}
		return payload
	}
	first := launch()
	second := launch()
	t.Logf("first:  %s", first)
	t.Logf("second: %s", second)
	if first != second {
		t.Errorf("a second launch on the same root changed the projection\nfirst:  %s\nsecond: %s", first, second)
	}
}

// TestReconcileDirPinsTheCurrentRoot is the control for the rule itself: the
// projection projects the directory it was last reconciled against. Two live
// directories with the same day would sum, but the second ReconcileDir — the
// state after an adopt, which leaves the old folder in place — drops the first
// root's rows instead.
func TestReconcileDirPinsTheCurrentRoot(t *testing.T) {
	auditIsolatedHome(t)
	dirA, dirB := testenv.TempDir(t), testenv.TempDir(t)
	auditWriteDay(t, dirA, "2026-09-01", map[string]any{"ts": "2026-09-01T02:00:00Z", "source": "desktop", "total": 100, "requests": 1})
	auditWriteDay(t, dirB, "2026-09-01", map[string]any{"ts": "2026-09-01T02:00:00Z", "source": "desktop", "total": 40, "requests": 1})
	ctx := context.Background()
	catalog, err := usagecatalog.Open(ctx, filepath.Join(testenv.TempDir(t), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	if err := catalog.ReconcileDir(ctx, dirA); err != nil {
		t.Fatal(err)
	}
	rows, err := catalog.Query(ctx, "2026-09-01", "2026-09-01", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Total != 100 {
		t.Fatalf("root A projection = %#v, want one row of 100", rows)
	}
	if _, err := os.Stat(dirA); err != nil {
		t.Fatalf("root A must stay live for this control: %v", err)
	}
	if err := catalog.ReconcileDir(ctx, dirB); err != nil {
		t.Fatal(err)
	}
	rows, err = catalog.Query(ctx, "2026-09-01", "2026-09-01", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Total != 40 {
		t.Fatalf("after reconciling root B the projection = %#v, want one row of 40 (not 140)", rows)
	}
}
