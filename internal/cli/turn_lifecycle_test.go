package cli

import (
	"testing"
	"time"

	"reasonix/internal/control"
	"reasonix/internal/event"
)

// TestControllerDispatchedTurnStartedEntersRunning pins the #9575 fix: when
// the controller auto-dispatches a queued follow-up, the TurnStarted event
// flips the composer into running state so an Enter queues instead of racing
// the dispatched turn, and the elapsed-tick chain re-arms.
func TestControllerDispatchedTurnStartedEntersRunning(t *testing.T) {
	ctrl := control.New(control.Options{})
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80)
	if m.state != tuiIdle {
		t.Fatalf("fresh TUI state = %v, want idle", m.state)
	}

	next, _ := m.Update(agentEventMsg(event.Event{Kind: event.TurnStarted}))
	m2 := next.(chatTUI)
	if m2.state != tuiRunning {
		t.Fatalf("dispatched TurnStarted left composer idle: %v", m2.state)
	}

	// A second TurnStarted while already running must not restart the clock.
	past := time.Now().Add(-5 * time.Minute)
	m2.runStart = past
	next, _ = m2.Update(agentEventMsg(event.Event{Kind: event.TurnStarted}))
	m3 := next.(chatTUI)
	if m3.runStart != past {
		t.Fatal("redundant TurnStarted restarted the elapsed timer")
	}
}
