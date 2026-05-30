package agent

import "reasonix/internal/provider"

// isThinkingModel reports whether a model name suggests thinking/reasoning
// mode capability. DeepSeek v4 and MiMo v2 models support reasoning_content.
func isThinkingModel(_ string) bool {
	// Most DeepSeek and MiMo models support reasoning.
	// The provider name (not model name) is what matters, but we check
	// the model string as a simple heuristic.
	return true // In v2, the providers we use all support reasoning round-trips.
}

// stripStaleReasoning removes reasoning_content from assistant messages that
// don't have tool_calls. Only tool-call assistant turns need reasoning for
// round-trip correctness with thinking-mode models; plain assistant reasoning
// just bloats future requests and can destabilize the cache prefix.
// Returns a new slice (does not mutate input) and the count of messages modified.
func stripStaleReasoning(msgs []provider.Message) ([]provider.Message, int) {
	cleaned := 0
	out := make([]provider.Message, len(msgs))
	for i, m := range msgs {
		out[i] = m
		if m.Role != provider.RoleAssistant {
			continue
		}
		if m.ReasoningContent == "" {
			continue
		}
		// Keep reasoning for tool-call turns (required for round-trip).
		if len(m.ToolCalls) > 0 {
			continue
		}
		// Strip reasoning from plain assistant messages.
		out[i].ReasoningContent = ""
		cleaned++
	}
	return out, cleaned
}
