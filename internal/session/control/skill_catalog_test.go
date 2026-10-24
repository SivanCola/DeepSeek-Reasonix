package control

import (
	"strings"
	"testing"

	"reasonix/internal/contract/event"
	"reasonix/internal/ext/skill"
)

// The listing is delivered once, on a user turn. A fold can summarise that turn
// away, and standing state that is only ever said once does not survive that —
// so the block is owed from the history, and a fold changes the answer.
func TestSkillCatalogIsOwedAgainAfterAFold(t *testing.T) {
	c := New(Options{Skills: []skill.Skill{{Name: "alpha", Description: "does the alpha thing"}}})
	defer c.Close()
	var visible projectionHistory
	c.bindProjectionSeer(visible.newest, visible.sees)

	first := c.Compose("hello")
	if !strings.Contains(first, "<available-skills>") || !strings.Contains(first, "alpha") {
		t.Fatalf("the first turn did not carry the listing: %q", first)
	}
	visible.deliver(first)
	second := c.Compose("again")
	if strings.Contains(second, "<available-skills>") {
		t.Fatalf("the listing was delivered twice without anything changing: %q", second)
	}
	visible.deliver(second)

	visible.forget() // the fold summarised the turn that carried it away
	c.sink.Emit(event.Event{Kind: event.CompactionDone, Compaction: event.Compaction{Messages: 4}})

	if after := c.Compose("after the fold"); !strings.Contains(after, "<available-skills>") || !strings.Contains(after, "alpha") {
		t.Fatalf("the fold left the session without a listing: %q", after)
	}
}

// A turn that composed and never reached the provider delivered nothing, so the
// debt stands until the composed text is part of the visible history.
func TestCatalogDebtStandsUntilTheTurnCarriesIt(t *testing.T) {
	c := New(Options{Skills: []skill.Skill{{Name: "alpha", Description: "does the alpha thing"}}})
	defer c.Close()
	var visible projectionHistory
	c.bindProjectionSeer(visible.newest, visible.sees)

	if first := c.Compose("hello"); !strings.Contains(first, "alpha") {
		t.Fatalf("the first turn did not carry the listing: %q", first)
	}
	abandoned := c.Compose("blocked before it ran")
	if !strings.Contains(abandoned, "alpha") {
		t.Fatalf("a composed-but-undelivered turn cleared the debt: %q", abandoned)
	}
	visible.deliver(abandoned)
	if delivered := c.Compose("and now"); strings.Contains(delivered, "<available-skills>") {
		t.Fatalf("the listing was re-sent after a turn carried it: %q", delivered)
	}
}

// A fold that folded nothing left the turn carrying the listing verbatim, so
// the listing is not owed again: re-sending it after every failed fold only
// grows the prompt the next attempt has to fold.
func TestSkillCatalogIsNotOwedAgainAfterAFoldThatFoldedNothing(t *testing.T) {
	c := New(Options{Skills: []skill.Skill{{Name: "alpha", Description: "does the alpha thing"}}})
	defer c.Close()
	var visible projectionHistory
	c.bindProjectionSeer(visible.newest, visible.sees)
	first := c.Compose("hello")
	if !strings.Contains(first, "<available-skills>") {
		t.Fatalf("the first turn did not carry the listing: %q", first)
	}
	visible.deliver(first)

	c.sink.Emit(event.Event{Kind: event.CompactionDone, Compaction: event.Compaction{Code: "summary_truncated"}})

	if after := c.Compose("after the failed fold"); strings.Contains(after, "<available-skills>") {
		t.Fatalf("a fold that folded nothing re-sent the listing: %q", after)
	}
}
