package billing

import (
	"strings"
	"time"
)

// LegacyUsageRecord is the minimum shape needed to migrate old telemetry.
type LegacyUsageRecord struct {
	SessionCost     float64
	SessionCurrency string
	// EndedAt is the session/turn end time used to pick historical ECB rates.
	EndedAt time.Time
	// Tokens optional for ledger completeness.
	PromptTokens     int
	CompletionTokens int
	ModelRef         string
	UsageSource      string
}

// MigrateLegacyUsage builds a CostQuote from pre-CostQuote telemetry.
// Records that cannot be valued are marked legacy_estimate; wiped mixed-currency
// zeros are not reconstructed from current price tables.
func MigrateLegacyUsage(rec LegacyUsageRecord, table *RateTable) CostQuote {
	cur := NormalizeCurrency(rec.SessionCurrency)
	if cur == "" {
		// Historic defaults often used ¥ symbol without code.
		if strings.Contains(rec.SessionCurrency, "¥") || strings.Contains(rec.SessionCurrency, "￥") {
			cur = "CNY"
		} else if strings.Contains(rec.SessionCurrency, "$") {
			cur = "USD"
		}
	}
	// Wiped mixed-currency totals (legacy code zeroed them) and empty rows must
	// not be reconstructed from current price tables.
	if rec.SessionCost <= 0 || cur == "" {
		reason := "legacy_unrecoverable"
		if rec.SessionCost == 0 && strings.TrimSpace(rec.SessionCurrency) != "" {
			// Explicit zero with a currency often means a prior mixed-currency wipe.
			reason = "legacy_wiped_or_zero"
		} else if rec.SessionCost < 0 {
			reason = "legacy_invalid_amount"
		}
		return CostQuote{
			Original:         MoneyOf(Zero, cur),
			Estimated:        true,
			Complete:         false,
			LegacyEstimate:   true,
			IncompleteReason: reason,
			ModelRef:         rec.ModelRef,
			UsageSource:      rec.UsageSource,
		}
	}
	amount := NewAmountFromFloat(rec.SessionCost)
	q := CostQuote{
		Original:       MoneyOf(amount, cur),
		Valuations:     map[string]Valuation{},
		Estimated:      true,
		Complete:       true,
		LegacyEstimate: true,
		ModelRef:       rec.ModelRef,
		UsageSource:    rec.UsageSource,
		BillingMode:    BillingModePAYG,
	}
	ended := rec.EndedAt
	if ended.IsZero() {
		ended = time.Now().UTC()
	}
	asOf := ended.UTC().Format("2006-01-02")
	q.Valuations[cur] = Valuation{
		Money:  q.Original,
		Basis:  BasisIdentity,
		Source: "legacy_telemetry",
		AsOf:   asOf,
	}
	if table != nil {
		for _, target := range []string{"CNY", "USD"} {
			if target == cur {
				continue
			}
			conv, snap, ok := table.Convert(amount, cur, target, ended, DefaultFXMaxAgeDays)
			if !ok {
				continue
			}
			q.Valuations[target] = Valuation{
				Money:  MoneyOf(conv, target),
				Basis:  BasisFX,
				Source: "legacy_ecb_backfill",
				AsOf:   snap.AsOf,
				Rate:   snap,
				Stale:  snap.Stale,
			}
			q.RateDate = snap.AsOf
		}
	}
	m := q.Original
	q.Selected = &m
	return q
}
