package session

import "reasonix/internal/provider"

const EventImageOffload = "image/offload"

func projectImageOffload(projection *Projection, ev Event) error {
	var body provider.ImageOffloadPayload
	if err := strictPayload(ev.Payload, &body); err != nil {
		return damagedPayload(ev, err)
	}
	projection.ModelMessages = provider.ApplyImageOffload(projection.ModelMessages, body.Targets)
	return nil
}
