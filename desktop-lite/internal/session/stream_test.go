package session

import (
	"errors"
	"testing"

	"reasonix/internal/event"
)

func TestTranslateEventRendersStreamingKinds(t *testing.T) {
	cases := []struct {
		name  string
		in    event.Event
		kind  string
		text  string
		tool  string
		level string
	}{
		{name: "text", in: event.Event{Kind: event.Text, Text: "hello"}, kind: "text", text: "hello"},
		{name: "reasoning", in: event.Event{Kind: event.Reasoning, Text: "thinking"}, kind: "reasoning", text: "thinking"},
		{name: "tool", in: event.Event{Kind: event.ToolDispatch, Tool: event.Tool{Name: "bash"}}, kind: "tool", tool: "bash"},
		{name: "info notice", in: event.Event{Kind: event.Notice, Text: "fyi"}, kind: "notice", text: "fyi", level: "info"},
		{name: "warn notice", in: event.Event{Kind: event.Notice, Text: "careful", Level: event.LevelWarn}, kind: "notice", text: "careful", level: "warn"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := TranslateEvent(tc.in)
			if !ok {
				t.Fatalf("TranslateEvent dropped a %s event", tc.name)
			}
			if got.Kind != tc.kind || got.Text != tc.text || got.Tool != tc.tool || got.Level != tc.level {
				t.Fatalf("TranslateEvent = %+v, want kind=%q text=%q tool=%q level=%q", got, tc.kind, tc.text, tc.tool, tc.level)
			}
		})
	}
}

// Operator notices describe runtime maintenance and must never reach the
// end-user transcript.
func TestTranslateEventDropsOperatorNotices(t *testing.T) {
	e := event.Event{Kind: event.Notice, Text: "lease renewed", Audience: event.NoticeAudienceOperator}

	if got, ok := TranslateEvent(e); ok {
		t.Fatalf("operator notice leaked into the transcript as %+v", got)
	}
}

func TestTranslateEventReportsCumulativeCacheHitRate(t *testing.T) {
	got, ok := TranslateEvent(event.Event{Kind: event.Usage, SessionHit: 9000, SessionMiss: 1000})
	if !ok {
		t.Fatal("usage event was dropped")
	}
	if !got.CacheKnown {
		t.Fatal("CacheKnown = false with cache tokens reported")
	}
	if got.CacheHitRate != 0.9 {
		t.Fatalf("CacheHitRate = %v, want 0.9", got.CacheHitRate)
	}
}

// A provider that reports no cache accounting is "unknown", not 0%.
func TestTranslateEventDistinguishesUnknownCacheFromZero(t *testing.T) {
	unknown, _ := TranslateEvent(event.Event{Kind: event.Usage})
	if unknown.CacheKnown {
		t.Fatalf("CacheKnown = true with no cache tokens: %+v", unknown)
	}

	genuineZero, _ := TranslateEvent(event.Event{Kind: event.Usage, SessionMiss: 1000})
	if !genuineZero.CacheKnown {
		t.Fatal("a reported all-miss session should be known, not unknown")
	}
	if genuineZero.CacheHitRate != 0 {
		t.Fatalf("CacheHitRate = %v, want 0", genuineZero.CacheHitRate)
	}
}

func TestTranslateEventCarriesTurnFailure(t *testing.T) {
	ok := func(e event.Event) Frame {
		f, rendered := TranslateEvent(e)
		if !rendered {
			t.Fatalf("TurnDone was dropped: %+v", e)
		}
		return f
	}

	if got := ok(event.Event{Kind: event.TurnDone}); got.Err != "" {
		t.Fatalf("successful turn carried an error: %+v", got)
	}
	if got := ok(event.Event{Kind: event.TurnDone, Err: errors.New("provider timeout")}); got.Err != "provider timeout" {
		t.Fatalf("Err = %q, want the failure text", got.Err)
	}
}

func TestTranslateEventDropsUnrenderedKinds(t *testing.T) {
	if got, ok := TranslateEvent(event.Event{Kind: event.TurnStarted}); ok {
		t.Fatalf("TurnStarted should not render a frame, got %+v", got)
	}
}
