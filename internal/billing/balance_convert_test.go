package billing

import (
	"strings"
	"testing"
	"time"
)

func TestConvertBalanceExactTargetOmitsApproximation(t *testing.T) {
	view := ConvertBalance(&Balance{Available: true, Infos: []Info{
		{Currency: "USD", TotalBalance: "9.82"},
	}}, "USD", nil, time.Now())
	if !view.Complete || view.DisplayApprox {
		t.Fatalf("exact target wallet = %+v, want complete non-approximate", view)
	}
	if got := view.DisplayApproxText(); got != "$9.82" {
		t.Fatalf("display = %q, want exact $9.82", got)
	}
}

func TestConvertBalanceConvertedTotalUsesApproximation(t *testing.T) {
	table := NewRateTable()
	table.SetObservation("2026-08-07", "USD", 1.1535)
	table.SetObservation("2026-08-07", "CNY", 7.7834)
	at := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	view := ConvertBalance(&Balance{Available: true, Infos: []Info{
		{Currency: "USD", TotalBalance: "9.82"},
		{Currency: "CNY", TotalBalance: "70.16"},
	}}, "USD", table, at)
	if !view.Complete || !view.DisplayApprox {
		t.Fatalf("converted wallet total = %+v, want complete approximate", view)
	}
	if got := view.DisplayApproxText(); !strings.HasPrefix(got, "≈$") {
		t.Fatalf("display = %q, want approximation prefix", got)
	}
}

func TestConvertBalancePartialNativeTotalStaysExact(t *testing.T) {
	view := ConvertBalance(&Balance{Available: true, Infos: []Info{
		{Currency: "USD", TotalBalance: "9.82"},
		{Currency: "CNY", TotalBalance: "70.16"},
	}}, "USD", nil, time.Now())
	if view.Complete || view.DisplayApprox {
		t.Fatalf("partial native total = %+v, want incomplete non-approximate", view)
	}
	if got := view.DisplayApproxText(); got != "$9.82" {
		t.Fatalf("display = %q, want exact known partial total", got)
	}
}
