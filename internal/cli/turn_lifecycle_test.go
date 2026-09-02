package cli

import (
	"errors"
	"testing"
	"time"

	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/sessioninbox"
)

type runningQueueController struct {
	control.SessionAPI
	req control.InboxRequest
	err error
}

func (c *runningQueueController) Running() bool { return true }

func (c *runningQueueController) EnqueueInbox(req control.InboxRequest) (sessioninbox.InboxReceipt, error) {
	c.req = req
	return sessioninbox.InboxReceipt{ItemID: "queued-item"}, c.err
}

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
	generation := m2.elapsedTickGeneration
	next, _ = m2.Update(agentEventMsg(event.Event{Kind: event.TurnStarted}))
	m3 := next.(chatTUI)
	if m3.runStart != past {
		t.Fatal("redundant TurnStarted restarted the elapsed timer")
	}
	if m3.elapsedTickGeneration != generation {
		t.Fatal("redundant TurnStarted started another elapsed-tick chain")
	}
}

func TestDrainedLifecyclePreservesOrder(t *testing.T) {
	tests := []struct {
		name     string
		initial  tuiState
		first    event.Kind
		buffered event.Kind
		want     tuiState
	}{
		{name: "next turn starts after done", initial: tuiRunning, first: event.TurnDone, buffered: event.TurnStarted, want: tuiRunning},
		{name: "fast turn finishes after start", initial: tuiIdle, first: event.TurnStarted, buffered: event.TurnDone, want: tuiIdle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := make(chan event.Event, 1)
			events <- event.Event{Kind: tt.buffered}
			m := newChatTUI(control.New(control.Options{}), "", events, 80)
			m.state = tt.initial

			next, _ := m.Update(agentEventMsg(event.Event{Kind: tt.first}))
			if got := next.(chatTUI).state; got != tt.want {
				t.Fatalf("final state = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDrainedTurnStartResetsBeforeNewUsage(t *testing.T) {
	events := make(chan event.Event, 2)
	events <- event.Event{Kind: event.TurnStarted}
	events <- event.Event{Kind: event.Usage, Usage: &provider.Usage{CompletionTokens: 3}}
	m := newChatTUI(control.New(control.Options{}), "", events, 80)
	m.state = tuiRunning
	m.turnTokens = 100

	next, _ := m.Update(agentEventMsg(event.Event{Kind: event.TurnDone}))
	m2 := next.(chatTUI)
	if m2.state != tuiRunning {
		t.Fatalf("queued turn state = %v, want running", m2.state)
	}
	if m2.turnTokens != 3 {
		t.Fatalf("queued turn tokens = %d, want 3", m2.turnTokens)
	}
}

func TestElapsedTickRejectsPriorTurnGeneration(t *testing.T) {
	m := newChatTUI(control.New(control.Options{}), "", make(chan event.Event, 1), 80)
	m.state = tuiRunning
	m.runStart = time.Now().Add(-30 * time.Second)
	m.elapsed = 7
	m.elapsedTickGeneration = 2

	next, cmd := m.Update(elapsedTickMsg{generation: 1})
	m2 := next.(chatTUI)
	if m2.elapsed != 7 {
		t.Fatalf("stale tick changed elapsed = %d, want 7", m2.elapsed)
	}
	if cmd != nil {
		t.Fatal("stale tick scheduled another timer")
	}
	next, _ = m2.Update(elapsedTickMsg{generation: 2})
	if got := next.(chatTUI).elapsed; got < 29 {
		t.Fatalf("current tick left elapsed = %d, want at least 29", got)
	}
}

func TestStartControllerTurnQueuesThroughSessionPort(t *testing.T) {
	ctrl := &runningQueueController{SessionAPI: control.New(control.Options{})}
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80)
	m.input.SetValue("draft")
	started := false

	cmd := m.startControllerTurn("expanded", "draft", func() { started = true })
	if cmd != nil || started {
		t.Fatalf("running controller started a competing turn: cmd=%v started=%v", cmd != nil, started)
	}
	if ctrl.req.Display != "expanded" || ctrl.req.Raw != "expanded" || ctrl.req.Submit != "expanded" {
		t.Fatalf("queued request = %+v, want expanded display/raw/submit", ctrl.req)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("successful queue left composer = %q", got)
	}
}

func TestStartControllerTurnRestoresComposerOnQueueFailure(t *testing.T) {
	wantErr := errors.New("queue unavailable")
	ctrl := &runningQueueController{SessionAPI: control.New(control.Options{}), err: wantErr}
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80)

	started := false
	cmd := m.startControllerTurn("expanded", "draft", func() { started = true })
	if cmd != nil || started {
		t.Fatalf("failed queue started a competing turn: cmd=%v started=%v", cmd != nil, started)
	}
	if got := m.input.Value(); got != "draft" {
		t.Fatalf("failed queue restored composer = %q, want draft", got)
	}
}
