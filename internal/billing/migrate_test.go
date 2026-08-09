package billing

import (
	"strings"
	"testing"
	"time"
)

func TestMigrateLegacyWipedCostIsIncomplete(t *testing.T) {
	q := MigrateLegacyUsage(LegacyUsageRecord{
		SessionCost:     0,
		SessionCurrency: "¥",
		EndedAt:         time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC),
	}, nil)
	if !q.LegacyEstimate || q.Complete {
		t.Fatalf("wiped legacy must be incomplete legacy_estimate: %+v", q)
	}
	if q.IncompleteReason != "legacy_wiped_or_zero" {
		t.Fatalf("reason = %q", q.IncompleteReason)
	}
}

func TestMigrateLegacyPositiveBackfillsFX(t *testing.T) {
	table, err := ParseECBHistCSV(strings.NewReader(sampleECB))
	if err != nil {
		t.Fatal(err)
	}
	q := MigrateLegacyUsage(LegacyUsageRecord{
		SessionCost:     7.85,
		SessionCurrency: "CNY",
		EndedAt:         time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	}, table)
	if !q.LegacyEstimate {
		t.Fatal("expected legacy_estimate")
	}
	if _, ok := q.Valuations["USD"]; !ok {
		t.Fatalf("expected USD backfill: %+v", q)
	}
}
