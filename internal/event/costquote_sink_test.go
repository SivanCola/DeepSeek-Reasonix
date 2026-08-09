package event

import (
	"testing"
	"time"

	"reasonix/internal/billing"
	"reasonix/internal/provider"
)

func TestQuoteContextReadsLatestFXCacheSnapshot(t *testing.T) {
	day := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)
	oldTable := billing.NewRateTable()
	oldTable.SetObservation("2026-03-07", "USD", 1)
	oldTable.SetObservation("2026-03-07", "CNY", 8)
	newTable := billing.NewRateTable()
	newTable.SetObservation("2026-03-07", "USD", 1)
	newTable.SetObservation("2026-03-07", "CNY", 4)

	cache := billing.NewFXCache("")
	cache.SetForTest(oldTable)
	ctx := &QuoteContext{DisplayCurrency: "USD", RateCache: cache, Now: func() time.Time { return day }}
	e := Event{
		Kind:    Usage,
		Usage:   &provider.Usage{PromptTokens: 1_000_000},
		Pricing: &provider.Pricing{Input: 8, Currency: "CNY"},
	}

	before := EnsureCostQuote(e, ctx)
	if before == nil || before.Selected == nil || before.Selected.Amount != "1" {
		t.Fatalf("old FX snapshot quote = %+v", before)
	}
	cache.SetForTest(newTable)
	after := EnsureCostQuote(e, ctx)
	if after == nil || after.Selected == nil || after.Selected.Amount != "2" {
		t.Fatalf("refreshed FX snapshot was not observed: %+v", after)
	}
}
