package pricing

import (
	"testing"
	"time"
)

func TestRequestRatesAreAppendOnlyAndSourced(t *testing.T) {
	seen := map[string]bool{}
	for _, row := range requestRates {
		key := row.Provider + "/" + row.Service
		if seen[key] {
			t.Fatalf("%s appears twice: a change is a new generation on its row, not a second row", key)
		}
		seen[key] = true
		if row.DocURL == "" || row.CheckedOn == "" || len(row.Rates) == 0 {
			t.Fatalf("%s lacks a source, a check date or a rate", key)
		}
		for _, r := range row.Rates {
			if r.Per1000 <= 0 || r.Currency == "" {
				t.Fatalf("%s carries an unusable rate %+v", key, r)
			}
		}
	}
}

func TestCurrentRequestRateForDeepSeekSearchAndFetch(t *testing.T) {
	for _, service := range []string{"search", "fetch"} {
		got, ok := CurrentRequestRate("deepseek", service)
		if !ok || got.Per1000 != 0.75 || got.Currency != "USD" || got.DocURL != DocDeepSeekPricing || got.CheckedOn == "" {
			t.Fatalf("%s = %+v (%v), want $0.75 per 1,000 successful requests from the pricing page", service, got, ok)
		}
	}
	if _, ok := CurrentRequestRate("deepseek", "nonsense"); ok {
		t.Fatal("an unknown service has a rate")
	}
}

func TestRequestCostIsPerSuccessfulRequest(t *testing.T) {
	rate, _ := CurrentRequestRate("deepseek", "search")
	if got := rate.Cost(1000); got != 0.75 {
		t.Fatalf("1,000 requests cost %v, want 0.75", got)
	}
	if got := rate.Cost(1); got != 0.00075 {
		t.Fatalf("1 request costs %v, want 0.00075", got)
	}
	if got := rate.Cost(0); got != 0 {
		t.Fatalf("no requests cost %v", got)
	}
}

// A per-request charge lands in the same ledger as token costs, under its own
// source, so the usage view can show what search cost apart from the model.
func TestRequestQuoteLandsInTheLedgerUnderItsOwnSource(t *testing.T) {
	rate, _ := CurrentRequestRate("deepseek", "search")
	at := time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC)
	q := RequestQuote(rate, 1, "deepseek-api/search", "deepseek-search")
	if q.Original.Currency != "USD" || q.Original.Amount != "0.00075" {
		t.Fatalf("original = %+v, want 0.00075 USD for one successful request", q.Original)
	}
	if !q.CostComplete || q.UsageSource != "deepseek-search" || q.ModelRef != "deepseek-api/search" {
		t.Fatalf("quote = %+v", q)
	}
	l := NewLedger()
	for range 3 {
		l.Add(q, UsageTokens{}, at)
	}
	l.Add(BuildQuote(QuoteInput{Usage: UsageTokens{PromptTokens: 1000, CompletionTokens: 10}, Rates: RateCard{Input: 1, Output: 2, Currency: "USD"}, ModelRef: "m", UsageSource: "executor"}), UsageTokens{PromptTokens: 1000}, at)
	var search LedgerEntry
	var sources int
	for _, e := range l.Entries {
		sources++
		if e.UsageSource == "deepseek-search" {
			search = e
		}
	}
	if sources != 2 || search.Quote.Original.Amount != "0.00225" {
		t.Fatalf("ledger = %+v (sources %d), want search summed apart from the model: 3 requests = 0.00225 USD", l.Entries, sources)
	}
}
