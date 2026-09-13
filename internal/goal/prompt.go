package goal

import (
	"encoding/json"
	"fmt"
)

type continuationEnvelope struct {
	GoalID        string  `json:"goalId"`
	Revision      uint64  `json:"revision"`
	Objective     string  `json:"objective"`
	Round         uint64  `json:"round"`
	MaxGoalRounds *uint64 `json:"maxGoalRounds"`
}

// ContinuationPrompt renders dynamic goal state into a one-shot user message.
// It must never be inserted into the system prompt: keeping the stable prefix
// unchanged preserves provider prompt-cache reuse across autonomous rounds.
func ContinuationPrompt(view View) (string, error) {
	if view.Phase != PhaseActive || view.Activation != ActivationArmed {
		return "", goalError(ErrInvalidTransition, "goal is not eligible for continuation")
	}
	payload, err := json.Marshal(continuationEnvelope{
		GoalID:        view.ID,
		Revision:      view.Revision,
		Objective:     view.Objective,
		Round:         view.RoundsStarted + 1,
		MaxGoalRounds: cloneLimit(view.MaxGoalRounds),
	})
	if err != nil {
		return "", fmt.Errorf("encode goal continuation: %w", err)
	}
	return "Continue making concrete progress on the current goal. Verify the work you perform.\n\n" +
		"<goal-round>\n" + string(payload) + "\n</goal-round>\n\n" +
		"Call get_goal before update_goal and use its exact goal_id and revision. " +
		"Mark complete only when the whole objective is done. If useful work remains, leave the goal active; " +
		"do not treat a round summary as completion. Mark blocked only for a concrete persistent blocker.", nil
}
