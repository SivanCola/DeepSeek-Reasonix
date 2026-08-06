package session

import (
	"reasonix/internal/event"
)

// Frame is one UI-visible unit of a turn, shaped for the webview rather than
// for the kernel.
//
// The kernel's event.Event carries eighteen-odd fields covering approvals,
// extensions, subagents, and telemetry. The lite shell renders a small subset,
// and translating here rather than in the Wails layer keeps that decision
// testable without a display: the shell only forwards what this produces.
type Frame struct {
	// Kind is the frame's render type: "text", "reasoning", "tool", "notice",
	// "usage", "turn_done", or "ready". Only "ready" originates in the shell
	// rather than in a kernel event — it tells the UI a session finished
	// assembling and the composer can accept input.
	Kind string `json:"kind"`
	// Text is the delta or message body for text, reasoning, and notice frames.
	Text string `json:"text,omitempty"`
	// Tool names the tool a "tool" frame refers to.
	Tool string `json:"tool,omitempty"`
	// Level is a notice's severity.
	Level string `json:"level,omitempty"`
	// Err is a turn_done frame's failure message; empty means the turn
	// succeeded.
	Err string `json:"err,omitempty"`
	// CacheHitRate is the session's cumulative prompt-cache hit rate on usage
	// frames, in [0,1]. It is cumulative rather than per-turn on purpose: a
	// single short turn or a compaction makes the per-turn number swing wildly,
	// while the session figure is the one that tracks what the user is paying.
	CacheHitRate float64 `json:"cacheHitRate"`
	// CacheKnown reports whether CacheHitRate is meaningful. A provider that
	// reports no cache tokens at all leaves it false, which the UI shows as
	// "unknown" rather than as a 0% hit rate.
	CacheKnown bool `json:"cacheKnown"`
}

// TranslateEvent maps a kernel event to a UI frame. ok is false for events the
// lite shell does not render, which the caller drops.
func TranslateEvent(e event.Event) (Frame, bool) {
	switch e.Kind {
	case event.Text:
		return Frame{Kind: "text", Text: e.Text}, true

	case event.Reasoning:
		return Frame{Kind: "reasoning", Text: e.Text}, true

	case event.ToolDispatch:
		return Frame{Kind: "tool", Tool: e.Tool.Name}, true

	case event.Notice:
		// Operator notices describe local runtime maintenance and must not be
		// forwarded as end-user chat messages, so the lite transcript drops
		// them rather than showing the user plumbing they cannot act on.
		if e.Audience == event.NoticeAudienceOperator {
			return Frame{}, false
		}
		return Frame{Kind: "notice", Text: e.Text, Level: levelName(e.Level)}, true

	case event.Usage:
		rate, known := cacheHitRate(e.SessionHit, e.SessionMiss)
		return Frame{Kind: "usage", CacheHitRate: rate, CacheKnown: known}, true

	case event.TurnDone:
		f := Frame{Kind: "turn_done"}
		if e.Err != nil {
			f.Err = e.Err.Error()
		}
		return f, true
	}
	return Frame{}, false
}

func levelName(l event.Level) string {
	if l == event.LevelWarn {
		return "warn"
	}
	return "info"
}

// cacheHitRate reports hit/(hit+miss). known is false when the provider
// reported no cache accounting at all, which is different from a genuine 0%.
func cacheHitRate(hit, miss int) (rate float64, known bool) {
	total := hit + miss
	if total <= 0 {
		return 0, false
	}
	return float64(hit) / float64(total), true
}
