package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/contract/config"
	"reasonix/internal/contract/event"
	"reasonix/internal/contract/provider"
	"reasonix/internal/state/stats"
)

// pricedTitleProvider answers a title request with text and one usage chunk,
// the way a real provider reports a call's tokens.
type pricedTitleProvider struct{ usage *provider.Usage }

func (p *pricedTitleProvider) Name() string { return "priced-title" }

func (p *pricedTitleProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 3)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "rename the login loop"}
	ch <- provider.Chunk{Type: provider.ChunkUsage, Usage: p.usage}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

// A title is a provider call on the person's account, so stock usage
// accounting must count its tokens and its money. The audit found the title
// path outside the CostQuoteSink every other recorder sits behind
// (title_provider.go:58), emitting a rate card but no quote. This drives the
// production function (tryGenerateTitle) and asserts the fixed contract.
func TestUsagePaidTotalCoversTitleGenerationSpend(t *testing.T) {
	s := newListenerTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t.Cleanup(func() {
		if err := stats.CloseUsageCatalogs(context.Background()); err != nil {
			t.Error(err)
		}
	})

	dir := config.StatsDir()
	if dir == "" {
		t.Fatal("config.StatsDir() is empty")
	}
	// The title model owns a recorder built straight from stats.NewRecorder, as
	// production does (title_provider.go:58). The quote must be attached by the
	// emitter, so it cannot depend on the sink having been wrapped.
	recorder := stats.NewRecorder(event.Discard, dir, "serve")

	// One ordinary turn call, quoted by the boot sink: 1M uncached input
	// tokens at $2/M = $2.
	quoted := event.NewCostQuoteSink(recorder, &event.QuoteContext{})
	quoted.Emit(event.Event{
		Kind:     event.Usage,
		ModelRef: "deepseek/deepseek-v4-pro",
		Usage:    &provider.Usage{PromptTokens: 1_000_000, CacheMissTokens: 1_000_000, TotalTokens: 1_000_000, RequestCount: 1},
		Pricing:  &provider.Pricing{Input: 2, Output: 8, Currency: "USD"},
	})

	// One title request: 1M uncached input tokens at $5/M = $5, on the title
	// model's own rate card.
	s.titleModel = titleProviderState{
		prov:  &pricedTitleProvider{usage: &provider.Usage{PromptTokens: 1_000_000, CacheMissTokens: 1_000_000, TotalTokens: 1_000_000, RequestCount: 1}},
		price: &provider.Pricing{Input: 5, Output: 10, Currency: "USD"},
		ref:   "deepseek/deepseek-v4-flash",
		sink:  recorder,
	}
	if got, _ := s.tryGenerateTitle(ctx, "rename the login loop"); got == "" {
		t.Fatal("title generation produced no title; the fixture did not reach the usage emit")
	}
	if err := stats.Flush(ctx, dir); err != nil {
		t.Fatalf("flush: %v", err)
	}

	day := time.Now().Format("2006-01-02")
	raw, err := os.ReadFile(filepath.Join(dir, day+".jsonl"))
	if err != nil {
		t.Fatalf("read stats file: %v", err)
	}
	var titleLine string
	var titleRow map[string]any
	for line := range strings.SplitSeq(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row map[string]any
		if json.Unmarshal([]byte(line), &row) != nil {
			continue
		}
		if row["usage_source"] == event.UsageSourceTitle {
			titleLine, titleRow = line, row
		}
	}
	if titleRow == nil {
		t.Fatalf("no usage_source=title row in %s", filepath.Join(dir, day+".jsonl"))
	}
	t.Logf("title row written = %s", titleLine)
	// The audit found this row unpriced (tokens and a request, no money, no
	// marker); a title is quoted at emit time, so the row carries its $5.
	if got := titleRow["cost_amount"]; got != "5" {
		t.Fatalf("title row cost_amount = %v, want the 1M uncached input tokens at $5/M ($5): %s", got, titleLine)
	}
	if got := titleRow["cost_currency"]; got != "USD" {
		t.Fatalf("title row cost_currency = %v, want USD (the title model's rate card): %s", got, titleLine)
	}
	if got := titleRow["cost_complete"]; got != true {
		t.Fatalf("title row cost_complete = %v, want true: the title model quotes its own rate card, so nothing about this call's money is unknown: %s", got, titleLine)
	}
	if _, hasReason := titleRow["incomplete_reason"]; hasReason {
		t.Fatalf("priced title row carries an incomplete_reason: %s", titleLine)
	}

	// The public surface: the Usage page asks GET /usage and labels the figure
	// "paid" unless the payload says the money is not complete.
	rec := httptest.NewRecorder()
	s.usage(rec, httptest.NewRequest(http.MethodGet, "/usage?from="+day+"&to="+day, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /usage = %d, body = %s", rec.Code, rec.Body.String())
	}
	t.Logf("GET /usage = %s", rec.Body.String())

	var got struct {
		Tokens   int64 `json:"tokens"`
		Requests int   `json:"requests"`
		Cost     []struct {
			Currency string `json:"currency"`
			Amount   string `json:"amount"`
		} `json:"cost"`
		CostEstimated  bool `json:"costEstimated"`
		CostIncomplete bool `json:"costIncomplete"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode /usage: %v; body = %s", err, rec.Body.String())
	}
	// The surface already says both calls happened...
	if got.Tokens != 2_000_000 || got.Requests != 2 {
		t.Fatalf("tokens/requests = %d/%d, want 2000000/2 (both calls counted); body = %s", got.Tokens, got.Requests, rec.Body.String())
	}
	// ...and the money it reports must cover both, not just the turn's call.
	if len(got.Cost) != 1 || got.Cost[0].Currency != "USD" || got.Cost[0].Amount != "7" {
		t.Fatalf("paid total = %v, want USD 7 (turn $2 + title $5); body = %s", got.Cost, rec.Body.String())
	}
	if got.CostIncomplete {
		t.Fatalf("costIncomplete = true for a range whose every call is priced; costEstimated = %v; body = %s",
			got.CostEstimated, rec.Body.String())
	}
}
