package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"reasonix/internal/contract/config"
	"reasonix/internal/contract/event"
	"reasonix/internal/contract/provider"
	"reasonix/internal/state/stats"
)

// moneyByCurrency indexes an API cost list by currency.
func moneyByCurrency(t *testing.T, list []statsMoney) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, entry := range list {
		out[entry.Currency] = entry.Amount
	}
	return out
}

// statsMoney mirrors pricing.Money's wire shape without importing it here.
type statsMoney struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
}

// A guardian review is a provider call on the person's account: the recorder
// already counts its tokens and requests (recorder.go:146-147). Its money is
// dropped there (the quote is nil, `e.Guardian.Pricing` is never read), so
// /usage reports a paid total that covers the turn's own calls but silently
// excludes every review the safety gate ran.
func TestUsagePaidTotalCoversGuardianReviewSpend(t *testing.T) {
	s := newListenerTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t.Cleanup(func() {
		if err := stats.CloseUsageCatalogs(context.Background()); err != nil {
			t.Error(err)
		}
	})
	dir := config.StatsDir()
	recorder := stats.NewRecorder(event.Discard, dir, "desktop")
	quoted := event.NewCostQuoteSink(recorder, &event.QuoteContext{})

	// One ordinary turn request: 1M uncached input tokens at $2/M = $2.
	quoted.Emit(event.Event{
		Kind:     event.Usage,
		ModelRef: "deepseek/deepseek-v4-pro",
		Usage:    &provider.Usage{PromptTokens: 1_000_000, CacheMissTokens: 1_000_000, TotalTokens: 1_000_000, RequestCount: 1},
		Pricing:  &provider.Pricing{Input: 2, Output: 8, Currency: "USD"},
	})
	// One guardian review of that turn's tool call: 1M uncached input tokens at
	// $5/M = $5. The event carries the rate card precisely so a display can
	// price the review.
	quoted.Emit(event.Event{
		Kind:     event.GuardianAssessment,
		ModelRef: "deepseek/deepseek-v4-flash",
		Guardian: event.GuardianResult{
			Outcome: "allow",
			Usage:   &provider.Usage{PromptTokens: 1_000_000, CacheMissTokens: 1_000_000, TotalTokens: 1_000_000, RequestCount: 1},
			Pricing: &provider.Pricing{Input: 5, Output: 10, Currency: "USD"},
		},
	})
	if err := stats.Flush(ctx, dir); err != nil {
		t.Fatalf("flush: %v", err)
	}

	day := time.Now().Format("2006-01-02")
	rec := httptest.NewRecorder()
	s.usage(rec, httptest.NewRequest(http.MethodGet, "/usage?from="+day+"&to="+day, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	t.Logf("GET /usage body = %s", rec.Body.String())

	var got struct {
		Tokens   int64        `json:"tokens"`
		Requests int          `json:"requests"`
		Cost     []statsMoney `json:"cost"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// Both calls are in the token total, so the surface already claims both
	// happened.
	if got.Tokens != 2_000_000 || got.Requests != 2 {
		t.Fatalf("tokens/requests = %d/%d, want 2000000/2 (both calls counted)", got.Tokens, got.Requests)
	}
	// The paid total must cover both provider calls it counted: $2 + $5.
	if have := moneyByCurrency(t, got.Cost); have["USD"] != "7" {
		t.Fatalf("paid total = %v, want USD 7 (the guardian review's $5 is missing); the range counts the review's 1M tokens and 1 request but none of its money", have)
	}
}
