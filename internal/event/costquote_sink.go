package event

import (
	"strings"
	"sync"
	"time"

	"reasonix/internal/billing"
	"reasonix/internal/provider"
)

// QuoteContext supplies display currency and FX for the CostQuote middleware.
type QuoteContext struct {
	mu sync.RWMutex
	// DisplayCurrency is the resolved ISO code (CNY|USD), not "auto".
	DisplayCurrency string
	// Rates is a static non-blocking snapshot used by tests and explicit hosts.
	Rates *billing.RateTable
	// RateCache is read for every quote so background refreshes become visible
	// without rebuilding the controller. Rates takes precedence when non-nil.
	RateCache *billing.FXCache
	// Now overrides the clock in tests.
	Now func() time.Time
	// BillingModeForModel resolves provider-owned billing semantics from the
	// immutable boot config. It keeps billing_mode authoritative even when a
	// custom provider name does not contain a token-plan heuristic.
	BillingModeForModel func(modelRef string) string
}

// SetDisplay updates the resolved display currency used for Selected.
func (c *QuoteContext) SetDisplay(currency string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.DisplayCurrency = billing.NormalizeCurrency(currency)
	c.mu.Unlock()
}

// SetRates installs an FX table snapshot.
func (c *QuoteContext) SetRates(rates *billing.RateTable) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.Rates = rates
	c.mu.Unlock()
}

func (c *QuoteContext) snapshot() (display string, rates *billing.RateTable, now time.Time) {
	if c == nil {
		return "", billing.GlobalRateTable(), time.Now().UTC()
	}
	c.mu.RLock()
	display = c.DisplayCurrency
	rates = c.Rates
	rateCache := c.RateCache
	nowFn := c.Now
	c.mu.RUnlock()
	if rates == nil && rateCache != nil {
		rates = rateCache.Read()
	}
	if rates == nil && rateCache == nil {
		rates = billing.GlobalRateTable()
	}
	if nowFn != nil {
		now = nowFn()
	} else {
		now = time.Now().UTC()
	}
	return display, rates, now
}

func (c *QuoteContext) billingMode(modelRef string) string {
	if c == nil {
		return ""
	}
	c.mu.RLock()
	resolve := c.BillingModeForModel
	c.mu.RUnlock()
	if resolve == nil {
		return ""
	}
	return strings.TrimSpace(resolve(modelRef))
}

// CostQuoteSink fills CostQuote on Usage events before forwarding. Frontends
// must consume e.CostQuote and must not call Pricing.Cost for aggregation.
type CostQuoteSink struct {
	Inner Sink
	Ctx   *QuoteContext
}

// NewCostQuoteSink wraps inner with quoting. A nil ctx still quotes original
// currency using GlobalRateTable when available.
func NewCostQuoteSink(inner Sink, ctx *QuoteContext) *CostQuoteSink {
	if ctx == nil {
		ctx = &QuoteContext{}
	}
	return &CostQuoteSink{Inner: inner, Ctx: ctx}
}

func (s *CostQuoteSink) Emit(e Event) {
	if s == nil {
		return
	}
	if e.Kind == Usage && e.Usage != nil && e.CostQuote == nil {
		e.CostQuote = EnsureCostQuote(e, s.Ctx)
	}
	if s.Inner != nil {
		s.Inner.Emit(e)
	}
}

// EnsureCostQuote builds a CostQuote for an event when missing.
func EnsureCostQuote(e Event, ctx *QuoteContext) *billing.CostQuote {
	if e.Usage == nil {
		return nil
	}
	display, rates, now := ctx.snapshot()
	if e.Pricing == nil {
		q := billing.CostQuote{
			Estimated: true, Complete: false, IncompleteReason: "no_price",
			ModelRef: e.ModelRef, UsageSource: e.UsageSource,
		}
		return &q
	}
	mode := billing.BillingModePAYG
	if configured := ctx.billingMode(e.ModelRef); configured != "" {
		mode = configured
	} else if strings.Contains(strings.ToLower(e.ModelRef), "token-plan") ||
		strings.Contains(strings.ToLower(e.Source), "token-plan") {
		mode = billing.BillingModeSubscriptionEquivalent
	}
	card := rateCardFromPricing(e.Pricing)
	q := billing.BuildQuote(billing.QuoteInput{
		Usage:           usageTokens(e.Usage),
		Rates:           card,
		OccurredAt:      now,
		DisplayCurrency: display,
		BillingMode:     mode,
		ModelRef:        e.ModelRef,
		UsageSource:     firstUsageSource(e),
		RatesTable:      rates,
	})
	return &q
}

func firstUsageSource(e Event) string {
	if s := strings.TrimSpace(e.UsageSource); s != "" {
		return s
	}
	if s := strings.TrimSpace(e.Source); s != "" {
		return s
	}
	return UsageSourceExecutor
}

func rateCardFromPricing(p *provider.Pricing) billing.RateCard {
	if p == nil {
		return billing.RateCard{}
	}
	return billing.RateCard{
		CacheHit: p.CacheHit,
		Input:    p.Input,
		Output:   p.Output,
		Currency: billing.NormalizeCurrency(p.Currency),
	}
}

func usageTokens(u *provider.Usage) billing.UsageTokens {
	if u == nil {
		return billing.UsageTokens{}
	}
	return billing.UsageTokens{
		PromptTokens:           u.PromptTokens,
		CompletionTokens:       u.CompletionTokens,
		CacheHitTokens:         u.CacheHitTokens,
		CacheMissTokens:        u.CacheMissTokens,
		CacheWriteTokens:       u.CacheWriteTokens,
		CacheWriteBilledTokens: u.CacheWriteBilledTokens,
		Estimated:              u.Estimated,
	}
}
