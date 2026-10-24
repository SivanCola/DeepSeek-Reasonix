package usagecatalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/base/testenv"
)

// entryFromRaw is the catalog's read of one JSONL line. It must apply the same
// rule the file read and the append path apply.
func TestEntryFromRawAppliesTheNoCostFactRule(t *testing.T) {
	t.Parallel()
	complete, incomplete := true, false
	cases := []struct {
		name string
		raw  rawRecord
		want bool
	}{
		{"quoted", rawRecord{Total: 100, CostAmount: "2", CostCurrency: "USD", CostComplete: &complete}, false},
		{"explicitly incomplete", rawRecord{Total: 100, CostAmount: "0", CostComplete: &incomplete}, true},
		{"no cost fact", rawRecord{Total: 100}, true},
		{"turn marker", rawRecord{Turn: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := entryFromRaw("2026-08-10", tc.raw).CostIncomplete; got != tc.want {
				t.Fatalf("entryFromRaw(%+v).CostIncomplete = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// The projection must carry the file read's verdict: a pre-schema row counts
// its tokens, contributes no money, and makes the day's rollup a floor. The
// priced sibling and the turn marker must not add to that count.
func TestAPreSchemaRowSurvivesReconcileAsIncomplete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := testenv.TempDir(t)
	path := filepath.Join(dir, "2026-08-10.jsonl")
	lines := []byte(
		"{\"ts\":\"2026-08-10T10:00:00Z\",\"model\":\"deepseek/m\",\"source\":\"desktop\",\"prompt\":42,\"total\":42,\"requests\":1}\n" +
			"{\"ts\":\"2026-08-10T11:00:00Z\",\"model\":\"deepseek/m\",\"source\":\"desktop\",\"total\":8,\"requests\":1,\"cost_amount\":\"1\",\"cost_currency\":\"USD\",\"cost_complete\":true}\n" +
			"{\"ts\":\"2026-08-10T12:00:00Z\",\"source\":\"desktop\",\"turn\":true}\n")
	if err := os.WriteFile(path, lines, 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err := Open(ctx, filepath.Join(testenv.TempDir(t), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })

	for pass := range 2 { // the second pass re-derives the day
		if err := catalog.ReconcileFile(ctx, path, "2026-08-10"); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		rows, err := catalog.Query(ctx, "2026-08-10", "2026-08-10", "desktop", "")
		if err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		var total, incomplete int64
		var costs []CostAmount
		for _, row := range rows {
			total += row.Total
			incomplete += row.CostIncomplete
			costs = append(costs, row.Costs...)
		}
		if total != 50 || incomplete != 1 {
			t.Fatalf("pass %d: total/incomplete = %d/%d, want 50/1 (the pre-schema row only)", pass, total, incomplete)
		}
		if len(costs) != 1 || costs[0].Currency != "USD" || costs[0].Amount != 1_000_000_000 {
			t.Fatalf("pass %d: costs = %#v, want the priced sibling's USD 1 (1e9 fixed-point units)", pass, costs)
		}
	}
}
