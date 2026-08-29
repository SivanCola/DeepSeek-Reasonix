package agent

import (
	"regexp"
	"strings"

	"reasonix/internal/provider"
)

var exitCodePattern = regexp.MustCompile(`(?i)exit (status|code)[:= ]+(-?\d+)`)

func errorCategory(toolName, errMsg string) string {
	msg := firstLine(errMsg)
	if strings.HasPrefix(msg, "argument_validation:") {
		return msg
	}
	if m := exitCodePattern.FindStringSubmatch(msg); len(m) == 3 {
		return toolName + ":exit:" + m[2]
	}
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline"):
		return toolName + ":transient:timeout"
	case strings.Contains(lower, "connection"):
		return toolName + ":transient:connection"
	case strings.Contains(lower, "fail") && strings.Contains(lower, "test"):
		return toolName + ":test_failure"
	}
	return toolName + ":" + msg
}

func (a *Agent) observeErrorCategory(call provider.ToolCall, err error) {
	if a == nil || err == nil {
		return
	}
	name := call.Name
	if call.ResolvedName != "" {
		name = call.ResolvedName
	}
	cat := errorCategory(name, err.Error())
	a.turn.loop.noteErrorCategory(cat)
}

func consecutiveNormalizedFailure(calls []provider.ToolCall, outcomes []toolOutcome, loop *turnLoopState) bool {
	if len(calls) == 0 || len(outcomes) == 0 || loop == nil {
		return false
	}
	name := calls[0].Name
	if calls[0].ResolvedName != "" {
		name = calls[0].ResolvedName
	}
	cat := errorCategory(name, outcomes[0].errMsg)
	return loop.errorCategoryCount(cat) >= 2 && (strings.Contains(cat, ":exit:") || strings.Contains(cat, ":test_failure"))
}
