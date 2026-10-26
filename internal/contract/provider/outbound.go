package provider

import (
	"encoding/json"
	"slices"

	"reasonix/internal/base/secrets"
)

// Signed names the provider-verified bytes an adapter sends back as it got
// them. Only the adapter that sends a signature knows it does, so each says;
// a receiver that cannot say, an extension or a broker, keeps nothing.
type Signed struct {
	Reasoning bool     // reasoning sent with its ReasoningSignature
	ToolCalls bool     // arguments of a call sent with its ThoughtSignature
	Items     []string // ResponsesItems of these types, replayed raw
}

// MaskOutbound masks every value the host holds for the person in a request
// an adapter is about to send; every adapter calls it, so no path to a model
// skips it. What keep names stays whole: altering it fails the request.
func MaskOutbound(req Request, keep Signed) Request {
	req.Messages = MaskMessages(req.Messages, keep)
	return req
}

// MaskMessages is MaskOutbound for messages leaving the host another way.
func MaskMessages(msgs []Message, keep Signed) []Message {
	m := secrets.Masker()
	if m == nil {
		return msgs
	}
	out := slices.Clone(msgs)
	for i := range out {
		out[i] = maskMessage(m, out[i], keep)
	}
	return out
}

func maskMessage(m *secrets.Mask, msg Message, keep Signed) Message {
	msg.Content = m.String(msg.Content)
	msg.ProviderContent = m.String(msg.ProviderContent)
	if !keep.Reasoning || msg.ReasoningSignature == "" {
		msg.ReasoningContent = m.String(msg.ReasoningContent)
	}
	if len(msg.ToolCalls) > 0 {
		calls := slices.Clone(msg.ToolCalls)
		for i := range calls {
			if !keep.ToolCalls || calls[i].ThoughtSignature == "" {
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
			if json.Unmarshal(it, &head) == nil && slices.Contains(keep.Items, head.Type) {
				items[i] = it
				continue
			}
			items[i] = m.JSON(it)
		}
		msg.ResponsesItems = items
	}
	return msg
}
