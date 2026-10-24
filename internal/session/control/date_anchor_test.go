package control

import (
	"strings"
	"testing"
	"time"
)

// The date is owed from the model-visible history, so composing alone delivers
// nothing and a fold that took the turn away re-owes it.
func TestDateAnchorOwesOncePerDayAndAgainAfterAFold(t *testing.T) {
	zone := time.FixedZone("UTC-5", -5*3600)
	now := time.Date(2026, 3, 1, 23, 30, 0, 0, zone)
	d := newDateAnchor(func() time.Time { return now })
	var visible projectionHistory
	d.debt.newest = visible.newest

	first := d.owed()
	if !strings.Contains(first, "2026-03-01 (Sunday)") || !strings.Contains(first, "UTC-05:00") {
		t.Fatalf("block = %q", first)
	}
	if again := d.owed(); again == "" {
		t.Fatal("a block that was composed but never carried must stay owed")
	}
	visible.deliver(wrapTurnBlock(currentDateTag, first))
	if got := d.owed(); got != "" {
		t.Fatalf("same day after the turn carried it = %q, want nothing owed", got)
	}

	now = now.Add(time.Hour)
	next := d.owed()
	if !strings.Contains(next, "2026-03-02 (Monday)") {
		t.Fatalf("after midnight = %q, want the new date", next)
	}
	visible.deliver(wrapTurnBlock(currentDateTag, next))

	visible.forget()
	if d.owed() == "" {
		t.Fatal("a fold that took the delivered turn out of view must re-owe the date")
	}
}

func TestDateAnchorWithoutAClockOwesNothing(t *testing.T) {
	d := newDateAnchor(nil)
	if got := d.owed(); got != "" {
		t.Fatalf("block = %q, want none", got)
	}
}
