package boot

// AUDIT (read-only candidate, not a fix): a removal is announced only while the
// controller still remembers having sent the block. A fold that keeps the block
// verbatim — because the turn carrying it is in the retained tail — clears that

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/contract/event"
	"reasonix/internal/contract/provider"
	"reasonix/internal/runtime/agent"
)

// foldMessages reports how many messages the last successful fold summarised,
// so an arm can prove the fold decided something rather than folding nothing.
func foldMessages(h *projectionHarness) (messages int, done bool) {
	h.events.mu.Lock()
	defer h.events.mu.Unlock()
	for _, e := range h.events.full {
		if e.Kind == event.CompactionDone {
			done = true
			messages = e.Compaction.Messages
		}
	}
	return messages, done
}

func TestAuditRemovalAfterAFoldThatKeptTheBlock(t *testing.T) {
	h := newProjectionHarness(t, "audit-removal-after-fold", "\ncompact_ratio = 0.4\n", "\ncontext_window = 12000\n")
	filler := strings.Repeat("filler sentence about the ledger. ", 200)
	small := strings.Repeat("short filler. ", 20)
	for i := range 5 {
		h.turn("turn-filler-" + string(rune('a'+i)) + " " + filler + " " + small)
	}

	// The block rides the newest turn, which the fold retains verbatim, so the
	// fold does not take the model's copy of it out of view.
	writeInstructions(t, h.dir, "REASONIX.md", "Rule one: never push on red.\n")
	carrier := h.turn("turn-carrier")
	if block := blockOf(projectionOf(t, carrier, "turn-carrier"), "project-instructions"); !strings.Contains(block, "Rule one") {
		t.Fatalf("the carrier turn did not deliver the instructions, so this arm measures nothing:\n%s", block)
	}

	if _, err := h.ctrl.Compact(context.Background(), agent.CompactRequest{}); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	messages, done := foldMessages(h)
	if !done || messages == 0 {
		t.Fatalf("no fold summarised anything, so this arm measures nothing: done=%v messages=%d", done, messages)
	}

	// Precondition: the fold left the carrier turn — and the rules inside it —
	// in the model's view. This is what makes silence wrong: the model reads
	// the old rules as standing state.
	if err := os.Remove(filepath.Join(h.dir, "REASONIX.md")); err != nil {
		t.Fatalf("remove the instructions: %v", err)
	}
	after := h.turn("turn-after-removal")
	projection := projectionOf(t, after, "turn-after-removal")
	stillVisible := ""
	for _, m := range after.Messages {
		if m.Role == provider.RoleUser && strings.Contains(m.Content, "Rule one: never push on red.") {
			stillVisible = m.Content
			break
		}
	}
	if stillVisible == "" {
		t.Fatalf("the fold took the carrier turn out of view, so silence would be right and this arm measures nothing")
	}
	if block := blockOf(projection, "project-instructions"); block == "" {
		t.Fatalf("the instructions were removed and the request carries no block saying so, while the model still reads them verbatim:\nstill visible: %q", stillVisible)
	} else if strings.Contains(block, "Rule one") {
		t.Errorf("the removal re-sent the rule it removed:\n%s", block)
	}
}
