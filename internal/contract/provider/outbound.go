package provider

import (
	"encoding/json"
	"slices"

	"reasonix/internal/base/secrets"
)

// MaskOutbound masks every value the host holds for the person in a request
// an adapter is about to send; every adapter calls it, so no path to a model
// skips it. Signed reasoning, replayed reasoning items and tool calls carrying
// a thought signature stay whole: the provider wrote and verifies those bytes.
func MaskOutbound(req Request) Request {
	m := secrets.Masker()
	if m == nil {
		return req
	}
	msgs := slices.Clone(req.Messages)
	for i := range msgs {
		msgs[i] = maskMessage(m, msgs[i])
	}
	req.Messages = msgs
	return req
}

func maskMessage(m *secrets.Mask, msg Message) Message {
	msg.Content = m.String(msg.Content)
	msg.ProviderContent = m.String(msg.ProviderContent)
	if msg.ReasoningSignature == "" {
		msg.ReasoningContent = m.String(msg.ReasoningContent)
	}
	if len(msg.ToolCalls) > 0 {
		calls := slices.Clone(msg.ToolCalls)
		for i := range calls {
			if calls[i].ThoughtSignature == "" {
				calls[i].Arguments = m.String(calls[i].Arguments)
			}
		}
		msg.ToolCalls = calls
	}
	if len(msg.ResponsesItems) > 0 {
		items := make([]json.RawMessage, len(msg.ResponsesItems))
		for i, it := range msg.ResponsesItems {
			var head struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(it, &head) == nil && head.Type == "reasoning" {
				items[i] = it
				continue
			}
			items[i] = m.JSON(it)
		}
		msg.ResponsesItems = items
	}
	return msg
}
