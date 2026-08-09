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
	t.runtimeCostDisplayCurrency = ""
	t.runtimeCostQuote = nil
	if t.usageTelemetry.CostLedger == nil || len(t.usageTelemetry.CostLedger.Entries) == 0 {
		if t.usageTelemetry.SessionCost <= 0 {
			return true
		}
		occurred := time.Now().UTC()
		quote := billing.MigrateLegacyUsage(billing.LegacyUsageRecord{
			SessionCost:     t.usageTelemetry.SessionCost,
			SessionCurrency: t.usageTelemetry.SessionCurrency,
			EndedAt:         occurred,
		})
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
		t.usageTelemetry.SessionCost = 0
		t.usageTelemetry.SessionCurrency = ""
		t.usageTelemetry.SessionCostUsd = 0
		return true
	}
	t.usageTelemetry.SessionCost = total.Selected.Float64()
	t.usageTelemetry.SessionCurrency = total.LegacyCurrencyCode()
	t.usageTelemetry.SessionCostUsd = t.usageTelemetry.SessionCost
	return true
}

// selectRuntimeDisplayCurrency applies an automatic wallet hint only to the
// live tab/session. It never mutates the persisted telemetry or configuration.
func (t *WorkspaceTab) selectRuntimeDisplayCurrency(display string) bool {
	if t == nil {
		return false
	}
	display = billing.NormalizeCurrency(display)
	t.telemMu.Lock()
	defer t.telemMu.Unlock()
	t.runtimeCostDisplayCurrency = display
	t.runtimeCostQuote = nil
	if t.usageTelemetry.CostLedger != nil && len(t.usageTelemetry.CostLedger.Entries) > 0 {
		total := t.usageTelemetry.CostLedger.SelectDisplay(display)
		t.runtimeCostQuote = &total
		return true
	}
	if t.usageTelemetry.SessionCost > 0 {
		quote := billing.MigrateLegacyUsage(billing.LegacyUsageRecord{
			SessionCost:     t.usageTelemetry.SessionCost,
			SessionCurrency: t.usageTelemetry.SessionCurrency,
			EndedAt:         time.Now().UTC(),
		})
		total := billing.AggregateQuotes([]billing.CostQuote{quote}, display)
		t.runtimeCostQuote = &total
	}
	return true
}

func (t *WorkspaceTab) clearRuntimeDisplayCurrency() {
	if t == nil {
		return
	}
	t.telemMu.Lock()
	t.runtimeCostDisplayCurrency = ""
	t.runtimeCostQuote = nil
	t.telemMu.Unlock()
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
