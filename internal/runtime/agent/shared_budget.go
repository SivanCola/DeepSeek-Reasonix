package agent

import (
	"context"
	"fmt"
	"reasonix/internal/contract/event"
	"reasonix/internal/contract/pricing"
	"reasonix/internal/contract/provider"
	"sync"
	"time"
)

type sharedBudgetKey struct{}

func (a *Agent) finalizeSharedBudget(state *turnRuntime) {
	if !state.graceRound {
		if axis, detail := state.sharedBudget.exceeded(); axis != "" {
			a.armFinalizationRound(state, landCause{kind: "task_budget", axis: axis, detail: detail})
		}
	}
}

type sharedBudget struct {
	mu       sync.Mutex
	limit    TaskBudget
	started  time.Time
	tokens   int
	currency string
	cost     map[string]pricing.Amount
}

func newSharedBudget(limit TaskBudget, rates *provider.Pricing) *sharedBudget {
	currency := ""
	if rates != nil {
		currency = pricing.NormalizeCurrency(rates.Currency)
		if currency == "" {
			currency = "CNY"
		}
	}
	return &sharedBudget{limit: limit, started: time.Now(), currency: currency, cost: map[string]pricing.Amount{}}
}

func (b *sharedBudget) observe(usage *provider.Usage, rates *provider.Pricing) {
	if b == nil || usage == nil {
		return
	}
	quote := event.EnsureCostQuote(event.Event{Kind: event.Usage, Usage: usage, Pricing: rates}, nil)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tokens += max(0, usage.PromptTokens) + max(0, usage.CompletionTokens)
	if quote != nil && quote.CostComplete {
		amount, err := pricing.ParseAmount(quote.Original.Amount)
		if err == nil {
			b.cost[quote.Original.Currency] = b.cost[quote.Original.Currency].Add(amount)
		}
	}
}

func (b *sharedBudget) exceeded() (string, string) {
	if b == nil {
		return "", ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit.Tokens > 0 && b.tokens >= b.limit.Tokens {
		return "token", fmt.Sprintf("task and delegated branches used %d tokens, reaching the shared %d budget", b.tokens, b.limit.Tokens)
	}
	if b.limit.Cost > 0 && b.currency != "" && b.cost[b.currency] >= pricing.NewAmountFromFloat(b.limit.Cost) {
		return "cost", fmt.Sprintf("task and delegated branches reached the shared %s %s budget", pricing.NewAmountFromFloat(b.limit.Cost), b.currency)
	}
	if b.limit.Wall > 0 && time.Since(b.started) >= b.limit.Wall {
		return "time", "task and delegated branches reached their shared time budget"
	}
	return "", ""
}

// InheritTaskBudget preserves the root spend owner when a background job changes context.
func InheritTaskBudget(dst, src context.Context) context.Context {
	if b, ok := src.Value(sharedBudgetKey{}).(*sharedBudget); ok {
		return context.WithValue(dst, sharedBudgetKey{}, b)
	}
	return dst
}

// CheckSharedTaskBudget refuses new work after the root and its branches exhaust a budget.
func CheckSharedTaskBudget(ctx context.Context) error {
	b, _ := ctx.Value(sharedBudgetKey{}).(*sharedBudget)
	if _, detail := b.exceeded(); detail != "" {
		return fmt.Errorf("%s", detail)
	}
	return nil
}

func (a *Agent) bindSharedBudget(ctx context.Context, state *turnRuntime) context.Context {
	b, _ := ctx.Value(sharedBudgetKey{}).(*sharedBudget)
	if b == nil {
		if a.task.sharedBudget == nil {
			a.task.sharedBudget = newSharedBudget(a.taskBudgetLimit(ctx), a.svc.pricing)
		}
		b = a.task.sharedBudget
	}
	state.sharedBudget = b
	return context.WithValue(ctx, sharedBudgetKey{}, b)
}
