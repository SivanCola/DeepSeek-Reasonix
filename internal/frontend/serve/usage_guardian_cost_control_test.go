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

// The production aggregate (the usage catalog, not the JSONL fallback) must not
// turn a review with no rate card into a free one. The tokens are counted; no
// amount is invented; the surface says its money is incomplete.
func TestUsageMarksAnUnpricedGuardianReviewIncomplete(t *testing.T) {
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

	quoted.Emit(event.Event{
		Kind:     event.GuardianAssessment,
		ModelRef: "deepseek/deepseek-v4-flash",
		Guardian: event.GuardianResult{
			Outcome: "allow",
			Usage:   &provider.Usage{PromptTokens: 1_000_000, CacheMissTokens: 1_000_000, TotalTokens: 1_000_000, RequestCount: 1},
			// No Pricing: the review's money is unknown, not zero.
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
		Tokens         int64        `json:"tokens"`
		Requests       int          `json:"requests"`
		Cost           []statsMoney `json:"cost"`
		CostIncomplete bool         `json:"costIncomplete"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Tokens != 1_000_000 || got.Requests != 1 {
		t.Fatalf("tokens/requests = %d/%d, want the unpriced review still counted", got.Tokens, got.Requests)
	}
	if len(got.Cost) != 0 {
		t.Fatalf("cost = %+v, want no amount invented without a rate card", got.Cost)
	}
	if !got.CostIncomplete {
		t.Fatalf("costIncomplete = false: GET /usage presents an unpriced review as costing nothing")
	}
}
