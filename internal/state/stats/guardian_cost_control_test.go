package stats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/base/testenv"
	"reasonix/internal/contract/event"
	"reasonix/internal/contract/provider"
)

// A review with no rate card must not read as a free review: no amount is
// invented, the row records that its money is unknown, and the aggregate says
// its cost total is a floor.
func TestGuardianReviewWithoutPricingIsIncompleteNotFree(t *testing.T) {
	dir := testenv.TempDir(t)
	r := NewRecorder(&spySink{}, dir, "desktop")
	r.Emit(event.Event{
		Kind:     event.GuardianAssessment,
		ModelRef: "deepseek/deepseek-v4-flash",
		Guardian: event.GuardianResult{
			Outcome: "allow",
			Usage:   &provider.Usage{PromptTokens: 1_000_000, CacheMissTokens: 1_000_000, TotalTokens: 1_000_000, RequestCount: 1},
			// No Pricing: the review's money is unknown, not zero.
		},
	})
	flushRecorder(t, r)

	day := dayStart(time.Now())
	got, err := r.writer.Query(SourceFilter{From: day, To: day})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got.Tokens != 1_000_000 || got.Requests != 1 {
		t.Fatalf("tokens/requests = %d/%d, want the unpriced review still counted", got.Tokens, got.Requests)
	}
	if len(got.Cost) != 0 {
		t.Fatalf("cost = %+v, want no amount invented without a rate card", got.Cost)
	}
	if !got.CostIncomplete {
		t.Fatalf("costIncomplete = false: an unpriced review is presented as costing nothing")
	}

	data, err := os.ReadFile(filepath.Join(dir, day.Format(dayLayout)+".jsonl"))
	if err != nil {
		t.Fatalf("read day file: %v", err)
	}
	var row record
	if err := json.Unmarshal([]byte(strings.Split(strings.TrimSpace(string(data)), "\n")[0]), &row); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if row.CostComplete == nil || *row.CostComplete {
		t.Fatalf("cost_complete = %v, want false for a review with no rate card", row.CostComplete)
	}
	if row.IncompleteReason != "no_price" {
		t.Fatalf("incomplete_reason = %q, want no_price", row.IncompleteReason)
	}
	if row.CostAmount != "" || row.CostCurrency != "" {
		t.Fatalf("cost = %q %q, want no amount", row.CostAmount, row.CostCurrency)
	}
}

// A guardian review joins the ordinary call's money exactly once: the same
// event supplies the tokens and the quote, and a turn call keeps its own price.
func TestGuardianReviewAddsOneQuoteAndNoDoubleCount(t *testing.T) {
	dir := testenv.TempDir(t)
	r := NewRecorder(&spySink{}, dir, "desktop")
	quoted := event.NewCostQuoteSink(r, &event.QuoteContext{})
	day := dayStart(time.Now())

	quoted.Emit(event.Event{
		Kind:     event.Usage,
		ModelRef: "deepseek/deepseek-v4-pro",
		Usage:    &provider.Usage{PromptTokens: 1_000_000, CacheMissTokens: 1_000_000, TotalTokens: 1_000_000, RequestCount: 1},
		Pricing:  &provider.Pricing{Input: 2, Output: 8, Currency: "USD"},
	})
	flushRecorder(t, r)
	ordinary, err := r.writer.Query(SourceFilter{From: day, To: day})
	if err != nil {
		t.Fatalf("query ordinary: %v", err)
	}
	if have := money(ordinary.Cost); len(have) != 1 || have["USD"] != "2" {
		t.Fatalf("ordinary cost = %v, want USD 2 unchanged", have)
	}
	if ordinary.Tokens != 1_000_000 || ordinary.Requests != 1 || ordinary.CostIncomplete {
		t.Fatalf("ordinary tokens/requests/incomplete = %d/%d/%v, want 1000000/1/false", ordinary.Tokens, ordinary.Requests, ordinary.CostIncomplete)
	}

	quoted.Emit(event.Event{
		Kind:     event.GuardianAssessment,
		ModelRef: "deepseek/deepseek-v4-flash",
		Guardian: event.GuardianResult{
			Outcome: "allow",
			Usage:   &provider.Usage{PromptTokens: 1_000_000, CacheMissTokens: 1_000_000, TotalTokens: 1_000_000, RequestCount: 1},
			Pricing: &provider.Pricing{Input: 5, Output: 10, Currency: "USD"},
		},
	})
	flushRecorder(t, r)
	both, err := r.writer.Query(SourceFilter{From: day, To: day})
	if err != nil {
		t.Fatalf("query both: %v", err)
	}
	if have := money(both.Cost); len(have) != 1 || have["USD"] != "7" {
		t.Fatalf("cost = %v, want USD 7 ($2 turn + $5 review, each once)", have)
	}
	if both.Tokens != 2_000_000 || both.Requests != 2 {
		t.Fatalf("tokens/requests = %d/%d, want 2000000/2 (no double count)", both.Tokens, both.Requests)
	}
	if both.CostIncomplete {
		t.Fatalf("costIncomplete = true, want both priced calls complete")
	}
}
