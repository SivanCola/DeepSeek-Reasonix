package boot

// Two entry points that change which messages exist after a once-delivered
// block was settled into the controller's projection debt. Both ask the same
// question at the provider boundary: after the conversation moves, does the

import (
	"strings"
	"testing"
	"time"

	"reasonix/internal/contract/provider"
	"reasonix/internal/session/control"
)

// rewind: the cut removes the only turn that carried the block, so the model's
// context has no copy at all and the next request owes a fresh one.
func TestProjectionRewindReOwesTheBlockTheCutRemoved(t *testing.T) {
	zone := time.FixedZone("UTC+8", 8*3600)
	now := time.Date(2026, 10, 10, 9, 0, 0, 0, zone)
	h := newProjectionHarnessAt(t, "ctxproj-rewind", "", "", func() time.Time { return now })

	first := h.turn("turn-alpha")
	if got := projectionOf(t, first, "turn-alpha"); !strings.Contains(got, "<current-date>") {
		t.Fatalf("the first turn did not carry the date, so this arm measures nothing:\n%s", got)
	}

	// Rewind the conversation to turn 0: its boundary is the first user message,
	// so this removes the only turn that ever carried the date.
	if err := h.ctrl.Rewind(0, control.RewindConversation); err != nil {
		t.Fatalf("Rewind(0, conversation): %v", err)
	}

	second := h.turn("turn-beta")
	got := projectionOf(t, second, "turn-beta")
	if !strings.Contains(got, "<current-date>") {
		t.Fatalf("freshness: the rewind removed the only turn carrying the date and the next request does not re-owe it; the model's context has no date at all:\n%q", got)
	}
}

// branch switch: the target branch still carries a copy, but an older one. The
// debt holds the newer bytes the *other* branch delivered, so the block is
// suppressed and the branch keeps reading a rule the user already replaced.
func TestProjectionBranchSwitchReOwesTheVersionTheBranchDoesNotHave(t *testing.T) {
	h := newProjectionHarness(t, "ctxproj-branch", "", "")
	h.ctrl.EnsureSessionPath()

	writeRule(t, h.dir, "style.md", "Use tabs, never spaces.\n")
	first := h.turn("turn-alpha")
	if block := blockOf(projectionOf(t, first, "turn-alpha"), "project-rules"); !strings.Contains(block, "Use tabs") {
		t.Fatalf("the first turn did not carry the rule, so this arm measures nothing:\n%s", block)
	}

	writeRule(t, h.dir, "style.md", "Use spaces, never tabs.\n")
	second := h.turn("turn-beta")
	if block := blockOf(projectionOf(t, second, "turn-beta"), "project-rules"); !strings.Contains(block, "Use spaces") {
		t.Fatalf("the edited rule never reached the session that edited it:\n%s", block)
	}

	// A branch cut at the end of turn 0, then the user switches to it. That
	// branch's history states the superseded rule and can never state the new
	// one unless the switch re-owes it.
	if _, err := h.ctrl.BranchAt(0, "b1"); err != nil {
		t.Fatalf("BranchAt(0): %v", err)
	}
	if _, err := h.ctrl.SwitchBranch("b1"); err != nil {
		t.Fatalf("SwitchBranch(b1): %v", err)
	}

	third := h.turn("turn-gamma")
	// The branch's own copy is what the model now reads: the superseded rule.
	replayed := ""
	for _, m := range third.Messages {
		if m.Role == provider.RoleUser && strings.Contains(m.Content, "Use tabs") {
			replayed = m.Content
		}
	}
	if replayed == "" {
		t.Fatal("the branch's superseded rule is not in the request, so this arm measures nothing")
	}
	got := projectionOf(t, third, "turn-gamma")
	block := blockOf(got, "project-rules")
	if !strings.Contains(block, "Use spaces") {
		t.Fatalf("freshness: the branch's history still states the superseded rule and the switch sent no replacement; the controller's debt was settled by the branch it left:\n%q", block)
	}
}
