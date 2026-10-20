// request_rates.go — what a vendor charges per successful request for a service
// that is not metered in tokens. Like officialRates, a change appends a
// generation and never edits one.
package pricing

import "strings"

// RequestRate is a price per 1,000 successful requests.
type RequestRate struct {
	Per1000  float64
	Currency string
}

// Cost is what n successful requests cost.
func (r RequestRate) Cost(n int) float64 {
	return r.Per1000 * float64(n) / 1000
}

// RequestRateInfo is a rate with where it was read and when.
type RequestRateInfo struct {
	RequestRate
	DocURL    string
	CheckedOn string
}

type requestRateRow struct {
	Provider  string
	Service   string
	DocURL    string
	CheckedOn string
	// Rates is oldest first; the last entry is what the service costs today.
	Rates []RequestRate
}

var requestRates = []requestRateRow{
	{Provider: "deepseek", Service: "search", DocURL: DocDeepSeekPricing, CheckedOn: "2026-10-17",
		Rates: []RequestRate{{Per1000: 0.75, Currency: "USD"}}},
	{Provider: "deepseek", Service: "fetch", DocURL: DocDeepSeekPricing, CheckedOn: "2026-10-17",
		Rates: []RequestRate{{Per1000: 0.75, Currency: "USD"}}},
}

// CurrentRequestRate is what a vendor's service costs per request today.
func CurrentRequestRate(provider, service string) (RequestRateInfo, bool) {
	provider, service = strings.ToLower(strings.TrimSpace(provider)), strings.ToLower(strings.TrimSpace(service))
	for _, row := range requestRates {
		if row.Provider == provider && row.Service == service {
			return RequestRateInfo{RequestRate: row.Rates[len(row.Rates)-1], DocURL: row.DocURL, CheckedOn: row.CheckedOn}, true
		}
	}
	return RequestRateInfo{}, false
}

// RequestQuote prices n successful requests as a CostQuote, so a per-request
// charge enters the ledger beside token costs under its own usage source.
func RequestQuote(rate RequestRateInfo, n int, modelRef, usageSource string) CostQuote {
	currency := NormalizeCurrency(rate.Currency)
	original := MoneyOf(NewAmountFromFloat(rate.Cost(n)), currency)
	q := CostQuote{
		Original: original,
		Valuations: map[string]Valuation{currency: {
			Money: original, Basis: BasisOfficialTable, Source: rate.DocURL, AsOf: rate.CheckedOn,
		}},
		Selected:        &original,
		BillingMode:     BillingModePAYG,
		CostComplete:    true,
		Coverage:        CoverageComplete,
		DisplayComplete: true,
		Complete:        true,
		DisplayStatus:   DisplayStatusMatched,
		AggregateMode:   AggregateModeSingleCurrency,
		ModelRef:        modelRef,
		UsageSource:     usageSource,
		CatalogSource:   rate.DocURL,
	}
	q.PricingFingerprint = PricingFingerprint(RateCard{Input: rate.Per1000, Currency: currency})
	return q
}
