package main

import (
	"time"

	"reasonix/internal/billing"
	"reasonix/internal/provider"
)

// selectDisplayCurrency rebinds occurrence-time valuations without repricing.
func (t *WorkspaceTab) selectDisplayCurrency(display string) bool {
	if t == nil {
		return false
	}
	t.telemMu.Lock()
	defer t.telemMu.Unlock()
	display = billing.NormalizeCurrency(display)
	if t.usageTelemetry.CostLedger == nil || len(t.usageTelemetry.CostLedger.Entries) == 0 {
		if t.usageTelemetry.SessionCost <= 0 {
			return true
		}
		occurred := time.Now().UTC()
		quote := billing.MigrateLegacyUsage(billing.LegacyUsageRecord{
			SessionCost:     t.usageTelemetry.SessionCost,
			SessionCurrency: t.usageTelemetry.SessionCurrency,
			EndedAt:         occurred,
		}, billing.GlobalRateTable())
		t.usageTelemetry.CostLedger = billing.NewLedger()
		t.usageTelemetry.CostLedger.Add(quote, billing.UsageTokens{
			PromptTokens:     t.usageTelemetry.PromptTokens,
			CompletionTokens: t.usageTelemetry.CompletionTokens,
		}, occurred)
	}
	total := t.usageTelemetry.CostLedger.SelectDisplay(display)
	t.usageTelemetry.SessionCostQuote = &total
	t.usageTelemetry.SessionCostComplete = total.Complete
	if total.Selected == nil {
		t.usageTelemetry.SessionCostComplete = false
		return true
	}
	t.usageTelemetry.SessionCost = total.Selected.Float64()
	t.usageTelemetry.SessionCurrency = total.LegacyCurrencyCode()
	t.usageTelemetry.SessionCostUsd = t.usageTelemetry.SessionCost
	return true
}

// repriceUsage preserves the legacy call shape as a display-only rebind.
func (t *WorkspaceTab) repriceUsage(pricingBySource map[string]*provider.Pricing) bool {
	_ = pricingBySource
	display := ""
	if t != nil {
		display = billing.NormalizeCurrency(t.usageTelemetry.SessionCurrency)
	}
	return t.selectDisplayCurrency(display)
}
