// rates.go — every rate this project has shipped for a vendor's model, and the
// two questions that asks: what it costs now, and what it used to cost.
package pricing

import (
	"slices"
	"strings"
	"time"
)

// rateGeneration is one published pair. Peak is nil for a generation the vendor
// billed at a single rate around the clock.
type rateGeneration struct {
	Base RateCard
	Peak *RateCard
	// From is the first day (YYYY-MM-DD, UTC) this generation is billed; empty
	// means from when it shipped. An announced change is appended with its date.
	From string
}

// today is the day a dated generation is read against; a test replaces it.
var today = func() string { return time.Now().UTC().Format("2006-01-02") }

// currentIndex is the generation in force today: the last one whose date has
// come. Rows with no dated generation answer with their last, as before.
func (r officialRateRow) currentIndex() int {
	day := today()
	for i := len(r.Rates) - 1; i > 0; i-- {
		if r.Rates[i].From == "" || r.Rates[i].From <= day {
			return i
		}
	}
	return 0
}

// officialRateRow is one vendor's model, in one currency, under one billing
// mode, with every rate this project has shipped for it.
type officialRateRow struct {
	Provider    string
	Model       string
	Currency    string
	BillingMode string
	Window      *PeakWindow
	DocURL      string
	Notes       string
	CheckedOn   string
	// Rates is oldest first; the last entry is what this costs today.
	Rates []rateGeneration
}

// officialRates is the price book. A price change appends a generation rather
// than editing one: that is what leaves the superseded rate where a stored price
// can still be recognised as this project's own default instead of a number its
// user chose, which is the difference between refreshing one and losing it.
var officialRates = []officialRateRow{
	{
		Provider: "deepseek", Model: "deepseek-flash", Currency: "CNY",
		BillingMode: BillingModePAYG, Window: deepseekPeak, DocURL: DocDeepSeekPricing, CheckedOn: deepseekCheckedOn,
		Rates: []rateGeneration{
			{Base: RateCard{CacheHit: 0.02, Input: 1, Output: 2, Currency: "CNY"}},
			{Base: RateCard{CacheHit: 0.05, Input: 1.5, Output: 4.5, Currency: "CNY"},
				Peak: &RateCard{CacheHit: 0.10, Input: 3.0, Output: 9.0, Currency: "CNY"}},
			{Base: RateCard{CacheHit: 0.02, Input: 1, Output: 4, Currency: "CNY"},
				Peak: &RateCard{CacheHit: 0.04, Input: 2, Output: 8, Currency: "CNY"}},
		},
	},
	{
		Provider: "deepseek", Model: "deepseek-flash", Currency: "USD",
		BillingMode: BillingModePAYG, Window: deepseekPeak, DocURL: DocDeepSeekPricing, CheckedOn: deepseekCheckedOn,
		Rates: []rateGeneration{
			{Base: RateCard{CacheHit: 0.0028, Input: 0.14, Output: 0.28, Currency: "USD"}},
			{Base: RateCard{CacheHit: 0.007, Input: 0.22, Output: 0.66, Currency: "USD"},
				Peak: &RateCard{CacheHit: 0.014, Input: 0.44, Output: 1.32, Currency: "USD"}},
			{Base: RateCard{CacheHit: 0.003, Input: 0.15, Output: 0.6, Currency: "USD"},
				Peak: &RateCard{CacheHit: 0.006, Input: 0.3, Output: 1.2, Currency: "USD"}},
		},
	},
	{
		Provider: "deepseek", Model: "deepseek-v4-pro", Currency: "CNY",
		BillingMode: BillingModePAYG, Window: deepseekPeak, DocURL: DocDeepSeekPricing, CheckedOn: deepseekCheckedOn,
		Rates: []rateGeneration{
			{Base: RateCard{CacheHit: 0.025, Input: 3, Output: 6, Currency: "CNY"}},
			{Base: RateCard{CacheHit: 0.15, Input: 4.5, Output: 13.5, Currency: "CNY"},
				Peak: &RateCard{CacheHit: 0.30, Input: 9.0, Output: 27.0, Currency: "CNY"}},
		},
	},
	{
		Provider: "deepseek", Model: "deepseek-v4-pro", Currency: "USD",
		BillingMode: BillingModePAYG, Window: deepseekPeak, DocURL: DocDeepSeekPricing, CheckedOn: deepseekCheckedOn,
		Rates: []rateGeneration{
			{Base: RateCard{CacheHit: 0.003625, Input: 0.435, Output: 0.87, Currency: "USD"}},
			{Base: RateCard{CacheHit: 0.022, Input: 0.66, Output: 1.98, Currency: "USD"},
				Peak: &RateCard{CacheHit: 0.044, Input: 1.32, Output: 3.96, Currency: "USD"}},
		},
	},

	// LongCat's launch discount, for which the vendor publishes no end date. List
	// price is CNY 0.10/5/20, USD 0.015/0.75/2.95 — what these revert to, and why
	// CheckedOn earns its place here.
	{
		Provider: "longcat", Model: "LongCat-2.0", Currency: "CNY",
		BillingMode: BillingModePAYG, DocURL: DocLongCatPricingCNY, Notes: "launch_discount_no_end_date", CheckedOn: checkedOn,
		Rates: []rateGeneration{{Base: RateCard{CacheHit: 0.04, Input: 2, Output: 8, Currency: "CNY"}}},
	},
	{
		Provider: "longcat", Model: "LongCat-2.0", Currency: "USD",
		BillingMode: BillingModePAYG, DocURL: DocLongCatPricingUSD, Notes: "launch_discount_no_end_date", CheckedOn: checkedOn,
		Rates: []rateGeneration{{Base: RateCard{CacheHit: 0.006, Input: 0.30, Output: 1.20, Currency: "USD"}}},
	},

	// Google: the two flash models double on 2027-01-01, hence the dated generation.
	// gemini-3.1-pro-preview is the rate up to 200k prompt tokens; gemini-4-argon has no price.
	{
		Provider: "google", Model: "gemini-3.8-flash", Currency: "USD",
		BillingMode: BillingModePAYG, DocURL: DocGeminiPricing, CheckedOn: googleCheckedOn,
		Rates: []rateGeneration{
			{Base: RateCard{CacheHit: 0.075, Input: 0.75, Output: 3.75, Currency: "USD"}},
			{From: "2027-01-01", Base: RateCard{CacheHit: 0.15, Input: 1.50, Output: 7.50, Currency: "USD"}},
		},
	},
	{
		Provider: "google", Model: "gemini-3.6-flash", Currency: "USD",
		BillingMode: BillingModePAYG, DocURL: DocGeminiPricing, CheckedOn: googleCheckedOn,
		Rates: []rateGeneration{
			{Base: RateCard{CacheHit: 0.075, Input: 0.75, Output: 3.75, Currency: "USD"}},
			{From: "2027-01-01", Base: RateCard{CacheHit: 0.15, Input: 1.50, Output: 7.50, Currency: "USD"}},
		},
	},
	{
		Provider: "google", Model: "gemini-3.5-flash-lite", Currency: "USD",
		BillingMode: BillingModePAYG, DocURL: DocGeminiPricing, CheckedOn: googleCheckedOn,
		Rates: []rateGeneration{{Base: RateCard{CacheHit: 0.03, Input: 0.30, Output: 2.50, Currency: "USD"}}},
	},
	{
		Provider: "google", Model: "gemini-3.1-pro-preview", Currency: "USD",
		BillingMode: BillingModePAYG, DocURL: DocGeminiPricing, Notes: "up_to_200k_prompt_tokens", CheckedOn: googleCheckedOn,
		Rates: []rateGeneration{{Base: RateCard{CacheHit: 0.20, Input: 2.00, Output: 12.00, Currency: "USD"}}},
	},

	// MiMo domestic PAYG; Token Plan bills the same rates as subscription_equivalent.
	{
		Provider: "mimo", Model: "mimo-v2.5-pro", Currency: "CNY",
		BillingMode: BillingModePAYG, DocURL: DocMiMoPAYG, CheckedOn: checkedOn,
		Rates: []rateGeneration{{Base: RateCard{CacheHit: 0.025, Input: 3, Output: 6, Currency: "CNY"}}},
	},
	{
		Provider: "mimo", Model: "mimo-v2.5", Currency: "CNY",
		BillingMode: BillingModePAYG, DocURL: DocMiMoPAYG, CheckedOn: checkedOn,
		Rates: []rateGeneration{{Base: RateCard{CacheHit: 0.02, Input: 1, Output: 2, Currency: "CNY"}}},
	},
	// Not on the vendor's price page as of CheckedOn. Kept because removing a rate
	// is what turns a still-configured model's cost silently into zero; a stale
	// rate at least reads as a number somebody can question.
	{
		Provider: "mimo", Model: "mimo-v2-flash", Currency: "CNY",
		BillingMode: BillingModePAYG, DocURL: DocMiMoPAYG, Notes: "not_on_vendor_page", CheckedOn: checkedOn,
		Rates: []rateGeneration{{Base: RateCard{CacheHit: 0.07, Input: 0.70, Output: 2.10, Currency: "CNY"}}},
	},
	{
		Provider: "mimo", Model: "mimo-v2.5-pro", Currency: "CNY",
		BillingMode: BillingModeSubscriptionEquivalent, DocURL: DocMiMoTokenPlan, Notes: "payg_equivalent_not_plan_bill", CheckedOn: checkedOn,
		Rates: []rateGeneration{{Base: RateCard{CacheHit: 0.025, Input: 3, Output: 6, Currency: "CNY"}}},
	},
	{
		Provider: "mimo", Model: "mimo-v2.5", Currency: "CNY",
		BillingMode: BillingModeSubscriptionEquivalent, DocURL: DocMiMoTokenPlan, Notes: "payg_equivalent_not_plan_bill", CheckedOn: checkedOn,
		Rates: []rateGeneration{{Base: RateCard{CacheHit: 0.02, Input: 1, Output: 2, Currency: "CNY"}}},
	},
	{
		Provider: "mimo", Model: "mimo-v2.6-pro", Currency: "CNY",
		BillingMode: BillingModePAYG, DocURL: DocMiMoPAYG, CheckedOn: mimoCheckedOn,
		Rates: []rateGeneration{{Base: RateCard{CacheHit: 0.025, Input: 3, Output: 6, Currency: "CNY"}}},
	},
	{
		Provider: "mimo", Model: "mimo-v2.6-flash", Currency: "CNY",
		BillingMode: BillingModePAYG, DocURL: DocMiMoPAYG, CheckedOn: mimoCheckedOn,
		Rates: []rateGeneration{{Base: RateCard{CacheHit: 0.02, Input: 1, Output: 2, Currency: "CNY"}}},
	},
	{
		Provider: "mimo", Model: "mimo-v2.6-pro-ultraspeed", Currency: "CNY",
		BillingMode: BillingModePAYG, DocURL: DocMiMoPAYG, CheckedOn: mimoCheckedOn,
		Rates: []rateGeneration{{Base: RateCard{CacheHit: 0.25, Input: 30, Output: 60, Currency: "CNY"}}},
	},
	{
		Provider: "mimo", Model: "mimo-v2.6-pro", Currency: "CNY",
		BillingMode: BillingModeSubscriptionEquivalent, DocURL: DocMiMoTokenPlan, Notes: "payg_equivalent_not_plan_bill", CheckedOn: mimoCheckedOn,
		Rates: []rateGeneration{{Base: RateCard{CacheHit: 0.025, Input: 3, Output: 6, Currency: "CNY"}}},
	},
	{
		Provider: "mimo", Model: "mimo-v2.6-flash", Currency: "CNY",
		BillingMode: BillingModeSubscriptionEquivalent, DocURL: DocMiMoTokenPlan, Notes: "payg_equivalent_not_plan_bill", CheckedOn: mimoCheckedOn,
		Rates: []rateGeneration{{Base: RateCard{CacheHit: 0.02, Input: 1, Output: 2, Currency: "CNY"}}},
	},
}

// retiredDeepSeekFlashModels are names DeepSeek still answers as its current
// flash model, at that model's price. Owned here because that is where the rates
// they bill at live; config reads them through the accessor below.
var retiredDeepSeekFlashModels = []string{"deepseek-v4-flash", "deepseek-v4-flash-vision-exp"}

// RetiredDeepSeekFlashModels copies the list on the way out: a caller sorting or
// trimming the result would otherwise change what every other caller sees.
func RetiredDeepSeekFlashModels() []string {
	return append([]string(nil), retiredDeepSeekFlashModels...)
}

// RateModelFor answers which model's rate a name is billed at — a retired name
// at the model that replaced it, an older spelling at the one it was renamed
// from. Scoped to billing on purpose: two models can share a rate without being
// the same model, and nothing outside a price lookup may read this as identity.
func RateModelFor(provider, model string) string {
	model = strings.TrimSpace(model)
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "deepseek":
		if slices.Contains(retiredDeepSeekFlashModels, model) {
			return "deepseek-flash"
		}
	case "mimo":
		switch model {
		case "mimo-v2-pro":
			return "mimo-v2.5-pro"
		case "mimo-v2-omni":
			return "mimo-v2.5"
		}
	}
	return model
}

// DefaultCurrency is what a vendor's rates are quoted in when an entry does not
// say. A vendor publishing one currency leaves no choice to make; DeepSeek
// prices in both, and USD is what an entry that never chose has been read as.
func DefaultCurrency(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	seen := map[string]bool{}
	for _, row := range officialRates {
		if row.Provider == provider {
			seen[row.Currency] = true
		}
	}
	if len(seen) == 1 {
		for currency := range seen {
			return currency
		}
	}
	return "USD"
}

// officialRateRowsFor finds the rows for a model, which is more than one when a
// vendor publishes the same rates under two billing modes.
func officialRateRowsFor(provider, model, currency string) []officialRateRow {
	provider, currency = strings.ToLower(strings.TrimSpace(provider)), NormalizeCurrency(currency)
	model = RateModelFor(provider, model)
	var out []officialRateRow
	for _, row := range officialRates {
		if row.Provider == provider && row.Model == model && row.Currency == currency {
			out = append(out, row)
		}
	}
	return out
}

// CurrentRate is what a vendor's model costs in a currency today — the off-peak
// card, which is what a caller charging one rate should quote. Nil when this
// catalog prices neither. A model published under two billing modes quotes the
// pay-as-you-go rate, which is the one an unsubscribed caller is charged.
func CurrentRate(provider, model, currency string) *RateCard {
	for _, row := range officialRateRowsFor(provider, model, currency) {
		if row.BillingMode == BillingModeSubscriptionEquivalent {
			continue
		}
		if len(row.Rates) > 0 {
			rate := row.Rates[row.currentIndex()].Base
			return &rate
		}
	}
	return nil
}

// IsOfficialCard reports whether card is a rate this project has shipped for the
// model in the card's own currency: any generation, its off-peak or its peak
// card. A card that is none of them is the user's, or damaged, and cannot be
// valued against the vendor's other-currency table.
func IsOfficialCard(provider, model string, card RateCard) bool {
	for _, row := range officialRateRowsFor(provider, model, card.Currency) {
		if row.BillingMode == BillingModeSubscriptionEquivalent {
			continue
		}
		for _, generation := range row.Rates {
			if sameCard(generation.Base, card) || generation.Peak != nil && sameCard(*generation.Peak, card) {
				return true
			}
		}
	}
	return false
}

func sameCard(a, b RateCard) bool {
	return a.CacheHit == b.CacheHit && a.Input == b.Input && a.Output == b.Output
}

// SupersededRates are the rates shipped for a model before the current one. A
// stored price matching one came from this project rather than from its user, so
// a refresh may replace it; anything else is theirs and is left alone.
func SupersededRates(provider, model, currency string) []RateCard {
	var out []RateCard
	for _, row := range officialRateRowsFor(provider, model, currency) {
		if row.BillingMode == BillingModeSubscriptionEquivalent || len(row.Rates) < 2 {
			continue
		}
		for _, generation := range row.Rates[:row.currentIndex()] {
			out = append(out, generation.Base)
		}
	}
	return out
}

// officialCatalogRows publishes the current generation of every row, plus the
// retired DeepSeek names, which bill at the flash rate under their own id.
func officialCatalogRows() []CatalogEntry {
	rows := make([]CatalogEntry, 0, len(officialRates)+len(retiredDeepSeekFlashModels)*2)
	for _, row := range officialRates {
		if len(row.Rates) == 0 {
			continue
		}
		current := row.Rates[row.currentIndex()]
		rows = append(rows, row.entry(row.Model, current))
		if row.Provider != "deepseek" || row.Model != "deepseek-flash" {
			continue
		}
		for _, retired := range retiredDeepSeekFlashModels {
			rows = append(rows, row.entry(retired, current))
		}
	}
	return rows
}

func (r officialRateRow) entry(model string, current rateGeneration) CatalogEntry {
	// Copied, so a caller holding a returned row cannot reach back into the
	// table every other caller reads.
	var peak *RateCard
	if current.Peak != nil {
		card := *current.Peak
		peak = &card
	}
	return CatalogEntry{
		Provider: r.Provider, Model: model, Currency: r.Currency,
		CacheHit: current.Base.CacheHit, Input: current.Base.Input, Output: current.Base.Output,
		Peak: peak, Window: r.Window, DocURL: r.DocURL,
		BillingMode: r.BillingMode, Notes: r.Notes, CheckedOn: r.CheckedOn,
	}
}
