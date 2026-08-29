package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/event"
)

type acceptedDecision struct {
	ID       string
	Question string
	Answer   string
}

func decisionIDForQuestions(qs []event.AskQuestion) string {
	h := sha256.New()
	for _, q := range qs {
		_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(q.Prompt))))
		_, _ = h.Write([]byte{0})
		for _, opt := range q.Options {
			_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(opt.Label))))
			_, _ = h.Write([]byte{0})
		}
	}
	return "dec-" + hex.EncodeToString(h.Sum(nil)[:8])
}

type turnStateContextKey struct{}

func withTurnState(ctx context.Context, turn *turnRuntime) context.Context {
	if turn == nil {
		return ctx
	}
	return context.WithValue(ctx, turnStateContextKey{}, turn)
}

func turnStateFrom(ctx context.Context) *turnRuntime {
	turn, _ := ctx.Value(turnStateContextKey{}).(*turnRuntime)
	return turn
}

func rememberDecision(ctx context.Context, id, question, answer string) {
	turn := turnStateFrom(ctx)
	if turn == nil || id == "" {
		return
	}
	turn.loop.rememberDecision(id, question, answer)
}

func existingDecision(ctx context.Context, id string) (acceptedDecision, bool) {
	turn := turnStateFrom(ctx)
	if turn == nil || id == "" {
		return acceptedDecision{}, false
	}
	return turn.loop.decision(id)
}

func firstExistingDecision(ctx context.Context) (acceptedDecision, bool) {
	turn := turnStateFrom(ctx)
	if turn == nil {
		return acceptedDecision{}, false
	}
	decisions := turn.loop.snapshotDecisions()
	if len(decisions) == 0 {
		return acceptedDecision{}, false
	}
	return decisions[0], true
}

type askArgs struct {
	DecisionID string `json:"decision_id"`
	Evidence   string `json:"new_evidence"`
	Questions  []struct {
		Header      string `json:"header"`
		Question    string `json:"question"`
		MultiSelect bool   `json:"multiSelect"`
		Options     []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"questions"`
}

func parseAskArgs(raw json.RawMessage) (askArgs, error) {
	var p askArgs
	if err := json.Unmarshal(raw, &p); err != nil {
		return askArgs{}, fmt.Errorf("invalid args: %w", err)
	}
	if len(p.Questions) == 0 {
		return askArgs{}, fmt.Errorf("at least one question is required")
	}
	if len(p.Questions) > 3 {
		return askArgs{}, fmt.Errorf("at most 3 questions may be asked in one clarification")
	}
	return p, nil
}
