package stats

import (
	"testing"
	"time"

	"reasonix/internal/base/testenv"
	"reasonix/internal/contract/event"
	"reasonix/internal/contract/provider"
)

// The money a guardian review cost is lost at record time, so the file read and
// the catalog read both understate it. This test reads the daily files directly
// (a non-production dir never acquires a usage catalog), which isolates the
// record from the projection.
func TestGuardianReviewSpendReachesTheFileRead(t *testing.T) {
	dir := testenv.TempDir(t)
	r := NewRecorder(&spySink{}, dir, "desktop")
	r.Emit(event.Event{
		Kind:     event.GuardianAssessment,
		ModelRef: "deepseek/deepseek-v4-flash",
		Guardian: event.GuardianResult{
			Outcome: "allow",
			Usage:   &provider.Usage{PromptTokens: 1_000_000, CacheMissTokens: 1_000_000, TotalTokens: 1_000_000, RequestCount: 1},
			Pricing: &provider.Pricing{Input: 5, Output: 10, Currency: "USD"},
		},
	})
	flushRecorder(t, r)

	day := dayStart(time.Now())
	got, err := r.writer.Query(SourceFilter{From: day, To: day})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got.Tokens != 1_000_000 || got.Requests != 1 {
		t.Fatalf("tokens/requests = %d/%d, want the review counted", got.Tokens, got.Requests)
	}
	// The audit's condition was inverted and it ended in an unconditional
	// failure, so it stayed red even once the row was priced. This asserts the
	// expected fact: the file read carries the review's $5.
	if have := money(got.Cost); len(have) != 1 || have["USD"] != "5" {
		t.Fatalf("cost = %+v, want the review's $5 priced from Guardian.Pricing (the event carried Pricing{Input:5,Output:10,Currency:USD})", got.Cost)
	}
	t.Logf("file read = tokens %d requests %d cost %+v", got.Tokens, got.Requests, got.Cost)
}
