package boot

// Controls for the visible-history rule: a block the model genuinely carries is
// not sent again, and a fold that took it out of view re-owes it.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"reasonix/internal/contract/event"
	"reasonix/internal/runtime/agent"
)

func TestProjectionDeliveredOnceIsNotRepeatedAndAFoldReOwesIt(t *testing.T) {
	h := newProjectionHarness(t, "ctxproj-delivery-control", "\ncompact_ratio = 0.99\n", "\ncontext_window = 12000\n")

	// A fold needs a history taller than the retained tail, and the turn that
	// carried the listing must be too tall to keep: otherwise the fold retains
	// it verbatim and re-sending it would be the duplicate this control forbids.
	filler := strings.Repeat("filler sentence about the ledger. ", 200)
	first := h.turn("turn-alpha " + filler)
	if blockOf(projectionOf(t, first, "turn-alpha"), "available-skills") == "" {
		t.Fatalf("the first turn carried no listing, so this control measures nothing")
	}

	steady := h.turn("turn-beta")
	if block := blockOf(projectionOf(t, steady, "turn-beta"), "available-skills"); block != "" {
		t.Fatalf("a listing that was genuinely delivered was sent again:\n%s", block)
	}

	small := strings.Repeat("short filler. ", 20)
	for i := range 6 {
		h.turn(fmt.Sprintf("turn-filler-%d %s", i, small))
	}
	if _, err := h.ctrl.Compact(context.Background(), agent.CompactRequest{IgnoreEconomics: true}); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if !h.events.saw(event.CompactionDone) {
		t.Fatalf("no fold completed, so this control measures nothing: %v", h.events.kinds)
	}

	after := h.turn("turn-eta")
	if block := blockOf(projectionOf(t, after, "turn-eta"), "available-skills"); block == "" {
		t.Fatalf("the fold took the delivered listing out of view and nothing re-owed it")
	}
}
