package control

import (
	"encoding/json"

	"reasonix/internal/event"
	"reasonix/internal/session"
)

func (c *Controller) terminalSessionEvents(e event.Event, projection session.Projection) ([]session.Event, error) {
	var out []session.Event
	if diagnostic, err := c.providerDiagnosticEvent(e); err != nil {
		return nil, err
	} else if diagnostic != nil {
		out = append(out, *diagnostic)
	}
	interactionState := "unavailable"
	if e.Cancelled || e.Status == event.TurnInterrupted {
		interactionState = "cancelled"
	}
	out = append(out, session.ClosureEvents(projection, interactionState, "turn ended before recording a result")...)
	if e.Recovery != nil && e.Recovery.State == "recovery_required" {
		payload, err := json.Marshal(e.Recovery)
		if err != nil {
			return nil, err
		}
		out = append(out, session.Event{Kind: "runtime/recovery", Payload: payload})
	}
	payload, err := json.Marshal(map[string]any{"status": terminalTurnStatus(e)})
	if err != nil {
		return nil, err
	}
	return append(out, session.Event{Kind: "turn/end", Payload: payload}), nil
}
