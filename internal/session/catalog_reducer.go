package session

import (
	"fmt"
	"strings"
)

// Retain only stable IDs and short authored previews. Upserts can erase the
// first preview, and history replacement can reorder it, so keeping only the
// first message would be incorrect. No message/tool/model body survives apply.
type catalogReducer struct {
	state     Projection
	turns     int
	positions map[string]int
	previews  []string
}

func (r *catalogReducer) apply(commit Commit) error {
	for _, ev := range commit.Events {
		one := commit
		one.Events = []Event{ev}
		// Reuse the canonical payload validators and turn/config semantics.
		if err := applyProjectionCommit(&r.state, one); err != nil {
			return err
		}
		if ev.Kind == "history/replace" || ev.Kind == "legacy/import" {
			r.positions = nil
			r.previews = nil
		}
		if r.positions == nil {
			r.positions = map[string]int{}
		}
		for _, message := range r.state.Messages {
			position, exists := r.positions[message.ID]
			if ev.Kind == "message/complete" && exists {
				return damagedPayload(ev, fmt.Errorf("duplicate stable message id %q", message.ID))
			}
			preview := strings.Clone(catalogMessagePreview(message))
			if ev.Kind == "message/upsert" && exists {
				r.previews[position] = preview
			} else {
				if !exists {
					r.positions[strings.Clone(message.ID)] = len(r.previews)
				}
				r.previews = append(r.previews, preview)
			}
		}
		for _, turn := range r.state.Turns {
			if turn.EndSequence != 0 {
				r.turns++
			}
		}
		// Keep only state used by subsequent metadata events. Body-heavy state,
		// closed turns and authority maps belong to runtime/history projections.
		s := r.state
		r.state = Projection{CommittedSequence: s.CommittedSequence, TurnID: s.TurnID,
			CurrentTurnStart: s.CurrentTurnStart, TurnStatus: s.TurnStatus,
			Title: s.Title, ModelRef: s.ModelRef, ModelIdentity: s.ModelIdentity}
	}
	return nil
}

func (r *catalogReducer) metadata(manifest Manifest) catalogMetadata {
	m := metadataFromProjection(manifest, r.state.CommittedSequence, r.state)
	m.Turns = r.turns
	for _, preview := range r.previews {
		if preview != "" {
			m.Preview = preview
			break
		}
	}
	return m
}
