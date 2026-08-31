package cli

import (
	"time"

	"reasonix/internal/control"
	"reasonix/internal/event"

	tea "charm.land/bubbletea/v2"
)

// startTurn commits the user bubble to scrollback, resets the turn accumulator,
// and kicks off the controller turn. `sent` goes to the model uncomposed (the
// controller frames it with any plan marker); `displayed` is what the transcript
// shows, and `restore` is what Esc puts back while the bubble is still deferred.
func (m *chatTUI) startTurn(sent, displayed, restore string) tea.Cmd {
	return m.startTurnWithRaw(sent, displayed, restore, sent)
}

// startTurnWithRaw is startTurn plus an explicit unresolved user prompt. This
// keeps reference-expanded model input separate from the text shown/restored by
// the frontend.
func (m *chatTUI) startTurnWithRaw(sent, displayed, restore, raw string) tea.Cmd {
	return m.startControllerTurn(displayed, restore, func() { m.ctrl.SendWithRaw(sent, raw) })
}

// startControllerTurn owns the TUI-side turn setup for controller entry points.
// Most prompts use SendWithRaw; slash-invoked skills use SubmitDisplay so the
// controller can choose inline vs isolated subagent execution from the live
// skill's RunAs metadata without the TUI reimplementing that policy.
func (m *chatTUI) startControllerTurn(displayed, restore string, start func()) tea.Cmd {
	// The composer can read idle while the controller already runs a
	// dispatched queued follow-up (TurnStarted not yet ingested): queue rather
	// than race the admission guard's silent drop (#9575).
	if c, ok := m.ctrl.(*control.Controller); ok && c.Running() {
		if _, err := m.enqueueFollowup(displayed, displayed); err != nil {
			m.notice("queue: " + err.Error())
		}
		return nil
	}
	// Flush any half-streamed leftover before the new turn (defensive).
	m.commitReasoning()
	m.commitPending()

	// Echo the user bubble to scrollback now so it appears the instant Enter is
	// pressed, not when the first packet lands: Esc before the reply pops it
	// back off and restores the text, leaving nothing stranded.
	m.pendingRestore = restore
	m.pendingPastes = m.pasteLabelsIn(restore)
	m.bubbleStartIdx = len(m.transcript)
	m.commitLine("") // blank line separating turns
	m.commitTranscriptSource(transcriptSource{
		kind: transcriptSourceUser, raw: displayed, planMode: m.planMode,
	})
	m.bubblePending = true
	m.turnDiscarded = false

	m.state = tuiRunning
	m.runStart = time.Now()
	m.elapsed = 0
	m.turnTokens = 0
	// The controller owns the run goroutine, its context, and cancellation; it
	// streams events to eventCh and emits TurnDone when the turn settles.
	m.noteWatchdogRunning()
	start()
	return tea.Batch(m.spinner.Tick, elapsedTick())
}

// confirmBubbleSent marks the already-echoed user bubble as really sent once a
// turn's first response packet arrives, so Esc no longer un-sends it (it cancels
// the stream instead). Also called defensively at turn end. A no-op once confirmed.
func (m *chatTUI) confirmBubbleSent() {
	if !m.bubblePending {
		return
	}
	m.bubblePending = false
	m.pendingRestore = ""
}

// drainAgentEvents ingests the events already buffered behind the first one:
// the producing goroutine has exited (a Cmd reads the channel once), so one
// re-wrap covers the whole batch instead of one per event.
func (m *chatTUI) drainAgentEvents(first event.Event) (turnDone, turnStarted, gitMaybeChanged bool) {
	turnDone = first.Kind == event.TurnDone
	turnStarted = first.Kind == event.TurnStarted
	gitMaybeChanged = first.Kind == event.ToolResult && !first.Tool.ReadOnly
	for range maxEventDrain {
		select {
		case e2 := <-m.eventCh:
			m.noteWatchdogHeartbeat(watchdogAgentSource(e2.Kind))
			m.ingestEvent(e2)
			switch {
			case e2.Kind == event.TurnDone:
				turnDone = true
			case e2.Kind == event.TurnStarted:
				turnStarted = true
			case e2.Kind == event.ToolResult && !e2.Tool.ReadOnly:
				gitMaybeChanged = true
			}
		default:
			return turnDone, turnStarted, gitMaybeChanged
		}
	}
	return turnDone, turnStarted, gitMaybeChanged
}

// noteControllerTurnStarted enters running state for a turn the TUI did not
// submit itself — the controller auto-dispatching a queued follow-up. Without
// it the composer reads as ready while the dispatched turn streams, so an
// Enter races the dispatch (silently dropped, or preempting the queue) and the
// elapsed-tick heartbeat chain stays dead (#9575).
func (m *chatTUI) noteControllerTurnStarted() tea.Cmd {
	if m.state == tuiRunning {
		return nil
	}
	m.state = tuiRunning
	m.runStart = time.Now()
	m.elapsed = 0
	m.turnTokens = 0
	m.noteWatchdogRunning()
	return tea.Batch(m.spinner.Tick, elapsedTick())
}
