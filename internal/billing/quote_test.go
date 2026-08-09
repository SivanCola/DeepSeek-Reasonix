package billing

import (
	"strings"
	"testing"
	"time"
)

func TestBuildQuoteDeepSeekFlashCNY(t *testing.T) {
	entry, ok := LookupCatalog("deepseek", "deepseek-v4-flash", "CNY", BillingModePAYG)
	if !ok {
		t.Fatal("missing catalog")
	}
	q := BuildQuote(QuoteInput{
		Usage:           UsageTokens{PromptTokens: 1_000_000, CompletionTokens: 1_000_000},
		Rates:           RateCardFromCatalog(entry),
		DisplayCurrency: "CNY",
		CatalogSource:   entry.DocURL,
	})
	// 1*1 + 2*1 = 3 CNY
	if q.Original.Amount != "3" || q.Original.Currency != "CNY" {
		t.Fatalf("original = %+v", q.Original)
	}
	if q.Selected == nil || q.Selected.Amount != "3" {
		t.Fatalf("selected = %+v", q.Selected)
	}
	if !q.Estimated {
		t.Fatal("expected estimated")
	}
}

func TestBuildQuoteDeepSeekProUSD(t *testing.T) {
	entry, ok := LookupCatalog("deepseek", "deepseek-v4-pro", "USD", BillingModePAYG)
	if !ok {
		t.Fatal("missing catalog")
	}
	q := BuildQuote(QuoteInput{
		Usage:           UsageTokens{CacheMissTokens: 1_000_000, CompletionTokens: 500_000},
		Rates:           RateCardFromCatalog(entry),
		DisplayCurrency: "USD",
	})
	// 0.435 + 0.435 = 0.87
	if q.Original.Currency != "USD" {
		t.Fatalf("currency = %s", q.Original.Currency)
	}
	got := q.Original.Float64()
	if got < 0.869 || got > 0.871 {
		t.Fatalf("cost = %v, want 0.87", got)
	}
}

func TestBuildQuoteLongCatAndMiMo(t *testing.T) {
	lc, ok := LookupCatalog("longcat", "LongCat-2.0", "CNY", "")
	if !ok {
		t.Fatal("longcat missing")
	}
	q := BuildQuote(QuoteInput{
		Usage: UsageTokens{PromptTokens: 1_000_000, CompletionTokens: 0},
		Rates: RateCardFromCatalog(lc),
	})
	if q.Original.Amount != "2" {
		t.Fatalf("longcat input cost = %s", q.Original.Amount)
	}
	mimo, ok := LookupCatalog("mimo", "mimo-v2.5-pro", "CNY", BillingModeSubscriptionEquivalent)
	if !ok {
		t.Fatal("mimo missing")
	}
	q2 := BuildQuote(QuoteInput{
		Usage:       UsageTokens{PromptTokens: 1_000_000},
		Rates:       RateCardFromCatalog(mimo),
		BillingMode: BillingModeSubscriptionEquivalent,
	})
	if q2.BillingMode != BillingModeSubscriptionEquivalent {
		t.Fatalf("mode = %s", q2.BillingMode)
	}
	if q2.Original.Amount != "3" {
		t.Fatalf("mimo equivalent = %s", q2.Original.Amount)
	}
}

func TestBuildQuoteOfficialTablePrefersRegionalPeer(t *testing.T) {
	// CNY official DeepSeek Flash → USD valuation must use official USD table,
	// not FX (no rate table provided).
	entry, ok := LookupCatalog("deepseek", "deepseek-v4-flash", "CNY", BillingModePAYG)
	if !ok {
		t.Fatal("missing CNY catalog")
	}
	q := BuildQuote(QuoteInput{
		Usage:           UsageTokens{PromptTokens: 1_000_000, CompletionTokens: 1_000_000},
		Rates:           RateCardFromCatalog(entry),
		DisplayCurrency: "USD",
		ProviderKind:    "deepseek",
		ModelID:         "deepseek-v4-flash",
		// RatesTable intentionally nil — official_table must still complete USD.
	})
	usd, ok := q.Valuations["USD"]
	if !ok {
		t.Fatal("missing USD valuation")
	}
	if usd.Basis != BasisOfficialTable {
		t.Fatalf("basis = %q, want official_table", usd.Basis)
	}
	// USD official: 0.14 + 0.28 = 0.42
	if usd.Money.Float64() < 0.419 || usd.Money.Float64() > 0.421 {
		t.Fatalf("official USD cost = %v, want 0.42", usd.Money.Float64())
	}
	if !q.Complete || q.Selected == nil || q.Selected.Currency != "USD" {
		t.Fatalf("quote incomplete: %+v", q)
	}
}

func TestBuildQuoteCustomPriceSkipsOfficialPeerUsesFX(t *testing.T) {
	table, err := ParseECBHistCSV(strings.NewReader(sampleECB))
	if err != nil {
		t.Fatal(err)
	}
	occurred, _ := time.Parse(time.RFC3339, "2026-03-07T12:00:00Z")
	q := BuildQuote(QuoteInput{
		Usage:           UsageTokens{PromptTokens: 1_000_000},
		Rates:           RateCard{Input: 9.99, Currency: "CNY"}, // custom, not catalog
		OccurredAt:      occurred,
		DisplayCurrency: "USD",
		ProviderKind:    "deepseek",
		ModelID:         "deepseek-v4-flash",
		RatesTable:      table,
	})
	usd, ok := q.Valuations["USD"]
	if !ok {
		t.Fatal("expected FX valuation for custom price")
	}
	if usd.Basis != BasisFX {
		t.Fatalf("basis = %q, want fx", usd.Basis)
	}
}

func TestBuildQuoteFXValuations(t *testing.T) {
	table, err := ParseECBHistCSV(strings.NewReader(sampleECB))
	if err != nil {
		t.Fatal(err)
	}
	occurred, _ := time.Parse(time.RFC3339, "2026-03-07T12:00:00Z")
	q := BuildQuote(QuoteInput{
		Usage:           UsageTokens{PromptTokens: 1_000_000},
		Rates:           RateCard{Input: 7.85, Currency: "CNY"},
		OccurredAt:      occurred,
		DisplayCurrency: "USD",
		RatesTable:      table,
	})
	if !q.Complete {
		t.Fatalf("incomplete: %s", q.IncompleteReason)
	}
	usd, ok := q.Valuations["USD"]
	if !ok {
		t.Fatal("missing USD valuation")
	}
	if usd.Basis != BasisFX || usd.Rate == nil {
		t.Fatalf("valuation = %+v", usd)
	}
	if q.Selected == nil || q.Selected.Currency != "USD" {
		t.Fatalf("selected = %+v", q.Selected)
	}
}

func TestBuildQuoteNoFXKeepsOriginal(t *testing.T) {
	q := BuildQuote(QuoteInput{
		Usage:           UsageTokens{PromptTokens: 1_000_000},
		Rates:           RateCard{Input: 1, Currency: "CNY"},
		DisplayCurrency: "USD",
		// no RatesTable
	})
	if q.Complete {
		t.Fatal("expected incomplete without FX")
	}
	if q.Original.Currency != "CNY" {
		t.Fatalf("original lost: %+v", q.Original)
	}
	if q.Selected != nil {
		t.Fatalf("selected should be nil, got %+v", q.Selected)
	}
}

func TestBuildQuoteOriginalDisplayDoesNotRequireUnusedFX(t *testing.T) {
	q := BuildQuote(QuoteInput{
		Usage:           UsageTokens{PromptTokens: 1_000_000},
		Rates:           RateCard{Input: 7, Currency: "CNY"},
		DisplayCurrency: "CNY",
		// No RatesTable: USD is unavailable, but the requested CNY total is exact.
	})
	if !q.Complete || q.Selected == nil || q.Selected.Amount != "7" || q.Selected.Currency != "CNY" {
		t.Fatalf("exact original display was marked incomplete: %+v", q)
	}
	if q.IncompleteReason != "" {
		t.Fatalf("unused USD valuation leaked incomplete reason %q", q.IncompleteReason)
	}
	if _, ok := q.Valuations["USD"]; ok {
		t.Fatalf("unexpected USD valuation without FX: %+v", q.Valuations["USD"])
	}
}

func TestAggregateQuotesCanRebindIncompleteDisplayToExactOriginal(t *testing.T) {
	q := BuildQuote(QuoteInput{
		Usage:           UsageTokens{PromptTokens: 1_000_000},
		Rates:           RateCard{Input: 7, Currency: "CNY"},
		DisplayCurrency: "USD",
	})
	if q.Complete {
		t.Fatal("USD display should be incomplete without FX")
	}
	agg := AggregateQuotes([]CostQuote{q}, "CNY")
	if !agg.Complete || agg.Selected == nil || agg.Selected.Amount != "7" || agg.Selected.Currency != "CNY" {
		t.Fatalf("aggregate did not recover exact original display: %+v", agg)
	}
}

func TestAggregateMixedCurrenciesWithSharedDisplay(t *testing.T) {
	table, _ := ParseECBHistCSV(strings.NewReader(sampleECB))
	occurred, _ := time.Parse(time.RFC3339, "2026-03-07T12:00:00Z")
	cny := BuildQuote(QuoteInput{
		Usage:      UsageTokens{PromptTokens: 1_000_000},
		Rates:      RateCard{Input: 7.85, Currency: "CNY"},
		OccurredAt: occurred, DisplayCurrency: "USD", RatesTable: table,
		ModelRef: "deepseek/flash", UsageSource: "executor",
	})
	usd := BuildQuote(QuoteInput{
		Usage:      UsageTokens{PromptTokens: 1_000_000},
		Rates:      RateCard{Input: 1.085, Currency: "USD"},
		OccurredAt: occurred, DisplayCurrency: "USD", RatesTable: table,
		ModelRef: "other/model", UsageSource: "subagent",
	})
	agg := AggregateQuotes([]CostQuote{cny, usd}, "USD")
	if !agg.Complete || agg.Selected == nil {
		t.Fatalf("agg incomplete: %+v", agg)
	}
	// ~1.085 + 1.085
	if agg.Selected.Float64() < 2.1 || agg.Selected.Float64() > 2.2 {
		t.Fatalf("agg selected = %v", agg.Selected.Float64())
	}
}

func TestAggregateQuotesRejectsPartialDisplayTotal(t *testing.T) {
	complete := CostQuote{
		Original:   MoneyOf(NewAmountFromFloat(1), "USD"),
		Valuations: map[string]Valuation{"USD": {Money: MoneyOf(NewAmountFromFloat(1), "USD")}},
		Complete:   true,
	}
	incomplete := CostQuote{
		Original:         MoneyOf(NewAmountFromFloat(7), "CNY"),
		Valuations:       map[string]Valuation{"CNY": {Money: MoneyOf(NewAmountFromFloat(7), "CNY")}},
		Complete:         false,
		IncompleteReason: "fx_unavailable",
	}
	agg := AggregateQuotes([]CostQuote{complete, incomplete}, "USD")
	if agg.Complete || agg.Selected != nil {
		t.Fatalf("partial display total must remain unavailable: %+v", agg)
	}
	if agg.IncompleteReason != "incomplete_valuations" {
		t.Fatalf("reason = %q, want incomplete_valuations", agg.IncompleteReason)
	}
}

func TestLedgerDoesNotDropMixedSources(t *testing.T) {
	table, _ := ParseECBHistCSV(strings.NewReader(sampleECB))
	occurred, _ := time.Parse(time.RFC3339, "2026-03-07T12:00:00Z")
	l := NewLedger()
	for _, src := range []string{"executor", "planner", "subagent", "compaction", "recovery-reviewer"} {
		cur := "CNY"
		if src == "subagent" {
			cur = "USD"
		}
		input := 1.0
		if cur == "USD" {
			input = 0.14
		}
		q := BuildQuote(QuoteInput{
			Usage:      UsageTokens{PromptTokens: 100_000},
			Rates:      RateCard{Input: input, Currency: cur},
			OccurredAt: occurred, DisplayCurrency: "CNY", RatesTable: table,
			ModelRef: "m/" + src, UsageSource: src,
		})
		l.Add(q, UsageTokens{PromptTokens: 100_000}, occurred)
	}
	total := l.Total("CNY")
	if total.Selected == nil || total.Selected.Float64() <= 0 {
		t.Fatalf("total lost: %+v", total)
	}
	if len(l.Entries) != 5 {
		t.Fatalf("entries = %d, want 5", len(l.Entries))
	}
	// Hot switch display currency — same ledger, different selection.
	usdTotal := l.Total("USD")
	if usdTotal.Selected == nil {
		t.Fatalf("USD total missing: %+v", usdTotal)
	}
	// Switching back must not clear.
	again := l.Total("CNY")
	if again.Selected.Amount != total.Selected.Amount {
		t.Fatalf("display switch mutated history: %s vs %s", again.Selected.Amount, total.Selected.Amount)
	}
}
