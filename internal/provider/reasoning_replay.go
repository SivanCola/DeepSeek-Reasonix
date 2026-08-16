package provider

import (
	"slices"
	"strings"

	"reasonix/internal/nilutil"
)

// AssistantReasoningReplayPolicy is optionally implemented by providers whose
// replay contract depends on the concrete assistant message. It extends the
// legacy tool-calls-only policy to provider-executed activity such as Anthropic
// server_tool_use without changing existing provider implementations.
type AssistantReasoningReplayPolicy interface {
	RequiresAssistantReasoningReplay(Message) bool
}

// RequiresAssistantReasoningReplay reports whether the exact provider-issued
// reasoning for m must survive storage and be replayed in later requests.
func RequiresAssistantReasoningReplay(p Provider, m Message) bool {
	if nilutil.IsNil(p) {
		return false
	}
	if policy, ok := p.(AssistantReasoningReplayPolicy); ok {
		return policy.RequiresAssistantReasoningReplay(m)
	}
	if RequiresReasoningRoundTrip(p) {
		return true
	}
	return len(m.ToolCalls) > 0 && RequiresToolCallReasoning(p)
}

// EmptyReasoningFallbackPolicy is optionally implemented by providers whose
// wire protocol accepts an explicit empty reasoning field for assistant tool
// turns. Anthropic thinking blocks do not have that fallback.
type EmptyReasoningFallbackPolicy interface {
	AllowsEmptyReasoningFallback() bool
}

// AllowsEmptyReasoningFallback defaults to false so unknown protocols never
// fabricate a replayable reasoning block.
func AllowsEmptyReasoningFallback(p Provider) bool {
	if nilutil.IsNil(p) {
		return false
	}
	policy, ok := p.(EmptyReasoningFallbackPolicy)
	return ok && policy.AllowsEmptyReasoningFallback()
}

// ProjectReplaySafeMessages returns the provider-visible projection for
// histories that contain assistant activity without the reasoning required to
// replay it. Canonical session messages are never modified. Healthy histories
// retain their backing slice so their wire bytes and prompt-cache prefix stay
// unchanged.
//
// For an unreplayable turn, visible assistant text is preserved as a plain
// message while provider-bound activity metadata and its contiguous client-tool
// results are omitted. Providers with an explicit empty-reasoning fallback do
// not need projection.
func ProjectReplaySafeMessages(p Provider, msgs []Message) ([]Message, bool) {
	if AllowsEmptyReasoningFallback(p) {
		return msgs, false
	}
	isUnreplayable := func(m Message) bool {
		return m.Role == RoleAssistant &&
			RequiresAssistantReasoningReplay(p, m) &&
			strings.TrimSpace(m.ReasoningContent) == ""
	}

	if !slices.ContainsFunc(msgs, isUnreplayable) {
		return msgs, false
	}

	out := make([]Message, 0, len(msgs))
	for i := 0; i < len(msgs); {
		m := msgs[i]
		if !isUnreplayable(m) {
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
			for i < len(msgs) && msgs[i].Role == RoleTool && !msgs[i].LocalOnly {
				i++
			}
		}
	}
	return out, true
}
