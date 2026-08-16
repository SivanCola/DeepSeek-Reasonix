package anthropic

import (
	"slices"
	"strings"

	"reasonix/internal/provider"
)

// projectDeepSeekReplaySafeMessages is the provider's final defense against
// serializing a tool/search turn that DeepSeek will reject. The Agent normally
// repairs legacy or interrupted history before it reaches this layer, but the
// provider is also called directly by probes and may be reached through hosts
// that do not own Agent's projection lifecycle.
//
// Healthy histories retain their backing slice so their wire bytes and prompt
// cache prefix stay unchanged. For an unreplayable turn, preserve only visible
// assistant text and drop the activity plus its contiguous client-tool results.
func projectDeepSeekReplaySafeMessages(msgs []provider.Message) []provider.Message {
	if !slices.ContainsFunc(msgs, missingDeepSeekActivityThinking) {
		return msgs
	}

	out := make([]provider.Message, 0, len(msgs))
	for i := 0; i < len(msgs); {
		m := msgs[i]
		if !missingDeepSeekActivityThinking(m) {
			out = append(out, m)
			i++
			continue
		}

		if strings.TrimSpace(m.Content) != "" {
			plain := m
			plain.ReasoningContent = ""
			plain.ReasoningSignature = ""
			plain.ReasoningID = ""
			plain.ReasoningStatus = ""
			plain.ToolCalls = nil
			plain.ResponsesItems = nil
			plain.ServerSearch = nil
			out = append(out, plain)
		}
		i++
		if len(m.ToolCalls) > 0 {
			for i < len(msgs) && msgs[i].Role == provider.RoleTool && !msgs[i].LocalOnly {
				i++
			}
		}
	}
	return out
}

func missingDeepSeekActivityThinking(m provider.Message) bool {
	return m.Role == provider.RoleAssistant &&
		(len(m.ToolCalls) > 0 || len(m.ServerSearch) > 0) &&
		strings.TrimSpace(m.ReasoningContent) == ""
}
