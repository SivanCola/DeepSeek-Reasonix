package stats

import (
	"fmt"
	"testing"
	"time"

	"reasonix/internal/base/testenv"
)

func hourOf(day time.Time, hour, minute int) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, day.Location())
}

// A single-day window is the only range where an hour axis means anything, so
// the query answers it with 24 buckets and keeps the daily row as the total.
func TestQuerySingleDayReturnsHourlyBuckets(t *testing.T) {
	dir := testenv.TempDir(t)
	w := NewWriter(dir)
	day := dayStart(time.Now())

	for _, rec := range []record{
		{Timestamp: hourOf(day, 9, 15), ModelRef: "deepseek/flash", Source: "desktop", Total: 100, CacheHit: 10, CacheMiss: 20, Requests: 1},
		{Timestamp: hourOf(day, 9, 45), ModelRef: "deepseek/flash", Source: "desktop", Total: 50, Requests: 1},
		{Timestamp: hourOf(day, 9, 0), Source: "desktop", Turn: true},
		{Timestamp: hourOf(day, 14, 20), ModelRef: "zhipu/glm", Source: "desktop", Total: 200, Requests: 1},
	} {
		if err := w.Append(rec); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got, err := w.Query(SourceFilter{From: day, To: day})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got.Hourly) != 24 {
		t.Fatalf("hourly buckets = %d, want 24", len(got.Hourly))
	}
	for h, bucket := range got.Hourly {
		if want := fmt.Sprintf("%02d:00", h); bucket.Hour != want {
			t.Fatalf("bucket %d = %q, want %q", h, bucket.Hour, want)
		}
	}
	if got.Hourly[9].Total != 150 || got.Hourly[9].Requests != 2 ||
		got.Hourly[9].Turns != 1 || got.Hourly[9].CacheHit != 10 || got.Hourly[9].CacheMiss != 20 {
		t.Fatalf("09:00 = %+v, want 150 tokens, 2 requests, 1 turn, cache 10/20", got.Hourly[9])
	}
	if got.Hourly[14].Total != 200 || got.Hourly[14].ByModel["zhipu/glm"] != 200 {
		t.Fatalf("14:00 = %+v, want zhipu/glm's 200 tokens", got.Hourly[14])
	}
	if got.Hourly[0].Total != 0 {
		t.Fatalf("00:00 = %+v, want an empty bucket", got.Hourly[0])
	}
	if len(got.Daily) != 1 || int64(got.Daily[0].Total) != got.Tokens {
		t.Fatalf("daily = %+v, tokens = %d; want one row carrying the total", got.Daily, got.Tokens)
	}
	var sum int
	for _, bucket := range got.Hourly {
		sum += bucket.Total
	}
	if int64(sum) != got.Tokens {
		t.Fatalf("hourly total = %d, daily total = %d", sum, got.Tokens)
	}
}

func TestQuerySingleDayHourlyHonoursSourceFilter(t *testing.T) {
	dir := testenv.TempDir(t)
	w := NewWriter(dir)
	day := dayStart(time.Now())

	for _, rec := range []record{
		{Timestamp: hourOf(day, 9, 0), ModelRef: "desktop/model", Source: "desktop", Total: 100, Requests: 1},
		{Timestamp: hourOf(day, 9, 30), ModelRef: "cli/model", Source: "cli", Total: 50, Requests: 1},
	} {
		if err := w.Append(rec); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got, err := w.Query(SourceFilter{From: day, To: day, Source: "cli"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got.Tokens != 50 || got.Hourly[9].Total != 50 || got.Hourly[9].ByModel["cli/model"] != 50 {
		t.Fatalf("filtered result = %+v; want only cli's 50 tokens in 09:00", got)
	}
}

// A multi-day window keeps its day series; an hour axis over several days
// would not name one point per x position.
func TestQueryMultiDayOmitsHourly(t *testing.T) {
	dir := testenv.TempDir(t)
	w := NewWriter(dir)
	day := dayStart(time.Now())

	for _, rec := range []record{
		{Timestamp: hourOf(day, 9, 0), ModelRef: "deepseek/flash", Total: 100, Requests: 1},
		{Timestamp: hourOf(day.AddDate(0, 0, -1), 14, 0), ModelRef: "deepseek/flash", Total: 50, Requests: 1},
	} {
		if err := w.Append(rec); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got, err := w.Query(SourceFilter{From: day.AddDate(0, 0, -1), To: day})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got.Daily) != 2 || len(got.Hourly) != 0 {
		t.Fatalf("daily = %d, hourly = %d; want 2 days and no hour buckets", len(got.Daily), len(got.Hourly))
	}
}
