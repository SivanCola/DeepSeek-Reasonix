package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/contract/config"
	"reasonix/internal/state/stats"
)

// A row written before the CostQuote schema (internal/stats/record.go at
// ee17c6290^) has no cost_amount, billing_mode or cost_complete: the fields did
// not exist yet. decodeRecords reads it like a current row and queryJSONL
// counts its tokens while adding no money and setting no flag — so the Usage
// page labels the result "paid" even though the older call's money is unknown.
func TestUsageCountsAPreCostSchemaRowButShowsNoMoney(t *testing.T) {
	s := newListenerTestServer(t)
	t.Cleanup(func() {
		if err := stats.CloseUsageCatalogs(context.Background()); err != nil {
			t.Error(err)
		}
	})
	dir := config.StatsDir()
	if dir == "" {
		t.Fatal("config.StatsDir() is empty")
	}
	day := time.Now().Format("2006-01-02")

	// Exactly the pre-schema shape: 1M tokens, no cost fields at all.
	legacy := `{"ts":"` + time.Now().Format(time.RFC3339) + `","model":"deepseek/deepseek-v4-flash","source":"desktop","prompt":1000000,"completion":0,"cache_hit":0,"cache_miss":1000000,"total":1000000,"requests":1}`
	// One row from the current schema, 1M uncached input tokens at $2/M.
	current := `{"ts":"` + time.Now().Format(time.RFC3339) + `","model":"deepseek/deepseek-v4-pro","source":"desktop","prompt":1000000,"cache_miss":1000000,"total":1000000,"requests":1,"cost_amount":"2","cost_currency":"USD","cost_complete":true,"billing_mode":"payg","display_status":"matched"}`
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, day+".jsonl"), []byte(legacy+"\n"+current+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

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
	if got.Tokens != 2_000_000 || got.Requests != 2 {
		t.Fatalf("tokens/requests = %d/%d, want both calls counted; body = %s", got.Tokens, got.Requests, rec.Body.String())
	}
	if !got.CostIncomplete {
		t.Fatalf("GET /usage counts the pre-schema call (requests=%d, tokens=%d) but reports cost=%v with costEstimated=%v and costIncomplete=%v: the older row's money is unknown and nothing in the payload says so",
			got.Requests, got.Tokens, got.Cost, got.CostEstimated, got.CostIncomplete)
	}
}
