package cli

import (
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/i18n"
	"reasonix/internal/provider"
)

// TestIngestEventRoutesByKind proves each event Kind lands in the right place:
// reasoning is consumed silently, while tool dispatch, blocked results, usage,
// notices, and coordinator phases each commit as their own scrollback line.
// Routing is by Kind, not by sniffing line prefixes.
func TestIngestEventRoutesByKind(t *testing.T) {
	// Reasoning is hidden from the transcript.
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.Reasoning, Text: "weighing options"})
	if len(*m.pendingCommit) != 0 {
		t.Errorf("reasoning should not commit, committed=%v", *m.pendingCommit)
	}
	if m.reasoning.Len() != 0 {
		t.Errorf("reasoning should not buffer visible text, got %q", m.reasoning.String())
	}

	for _, tc := range []struct {
		name string
		ev   event.Event
		want string
	}{
		{"dispatch", event.Event{Kind: event.ToolDispatch, Tool: event.Tool{Name: "read_file", Args: `{"path":"x"}`}}, "  -> read_file {\"path\":\"x\"}"},
		{"blocked", event.Event{Kind: event.ToolResult, Tool: event.Tool{Name: "bash", Err: "blocked by permission policy"}}, "  ⊘ bash blocked by permission policy"},
		{"usage", event.Event{Kind: event.Usage, Usage: &provider.Usage{PromptTokens: 1000, CompletionTokens: 200, TotalTokens: 1200, CacheHitTokens: 900, CacheMissTokens: 100}}, "  · 1200 token"},
		{"notice-info", event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "compacted 8 messages → summary"}, "  · compacted 8 messages → summary"},
		{"notice-warn", event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: "response truncated: hit max output tokens"}, "  ! response truncated: hit max output tokens"},
		{"phase", event.Event{Kind: event.Phase, Text: "planner · planning"}, "[planner · planning]"},
	} {
		m := newTestChatTUI()
		m.ingestEvent(tc.ev)
		got := *m.pendingCommit
		if len(got) != 1 || !strings.Contains(got[0], tc.want) {
			t.Errorf("%s: committed=%v, want a single line containing %q", tc.name, got, tc.want)
		}
	}

	// A successful tool result is silent — it only feeds the model.
	m = newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{Name: "read_file", Output: "contents"}})
	if len(*m.pendingCommit) != 0 {
		t.Errorf("successful tool result should be silent, committed=%v", *m.pendingCommit)
	}
}

// TestAnswerTextStartingWithBracketStaysInAnswer locks in the win of the typed
// event stream: model answer text starting with "[" — a markdown link, a slice
// literal, even a quoted "[… · planning]" — is a Text event, so it can never be
// mistaken for a coordinator phase marker the way prefix-sniffing a flattened
// byte stream once could. It stays in the answer buffer and renders as markdown.
func TestAnswerTextStartingWithBracketStaysInAnswer(t *testing.T) {
	for _, txt := range []string{
		"[link](https://example.com)",
		"[1, 2, 3]",
		"[planner · planning] (the model quoting a marker)",
	} {
		m := newTestChatTUI()
		m.ingestEvent(event.Event{Kind: event.Text, Text: txt})
		if len(*m.pendingCommit) != 0 {
			t.Errorf("answer text %q should stay live, not commit as an event line: %v", txt, *m.pendingCommit)
		}
		if m.pending.String() != txt {
			t.Errorf("answer text should buffer verbatim, got %q want %q", m.pending.String(), txt)
		}
	}
}

func TestUsageCommitsSingleLightweightFooter(t *testing.T) {
	m := newTestChatTUI()
	m.width = 80

	m.ingestEvent(event.Event{Kind: event.Usage, Usage: &provider.Usage{
		PromptTokens:     1000,
		CompletionTokens: 200,
		TotalTokens:      1200,
		CacheHitTokens:   900,
		CacheMissTokens:  100,
	}, Pricing: &provider.Pricing{CacheHit: 0.02, Input: 1, Output: 2, Currency: "¥"}})

	got := *m.pendingCommit
	if len(got) != 1 {
		t.Fatalf("usage should commit one footer line, got %d lines: %q", len(got), got)
	}
	if strings.Contains(got[0], "──") || strings.Contains(got[0], " │ ") {
		t.Fatalf("usage footer should be lightweight, got %q", got[0])
	}
	for _, want := range []string{"1200 token", "cache 90%", "¥0.0005"} {
		if !strings.Contains(got[0], want) {
			t.Fatalf("usage footer = %q, want %q", got[0], want)
		}
	}
}

func TestUsageFooterColorFollowsCacheHealth(t *testing.T) {
	for _, tc := range []struct {
		name string
		u    *provider.Usage
		want string
	}{
		{"high cache", &provider.Usage{PromptTokens: 1000, TotalTokens: 1200, CacheHitTokens: 900, CacheMissTokens: 100}, usageFooterGreen},
		{"medium cache", &provider.Usage{PromptTokens: 1000, TotalTokens: 1200, CacheHitTokens: 600, CacheMissTokens: 400}, usageFooterYellow},
		{"low cache", &provider.Usage{PromptTokens: 1000, TotalTokens: 1200, CacheHitTokens: 300, CacheMissTokens: 700}, usageFooterOrange},
		{"unknown cache", &provider.Usage{PromptTokens: 0, TotalTokens: 1200}, usageFooterGray},
	} {
		if got := usageFooterColor(tc.u, nil); got != tc.want {
			t.Errorf("%s: color=%q want %q", tc.name, got, tc.want)
		}
	}

	diag := &event.CacheDiagnostics{PrefixChanged: true, PrefixChangeReasons: []string{"tools schema changed"}}
	u := &provider.Usage{PromptTokens: 1000, TotalTokens: 1200, CacheHitTokens: 900, CacheMissTokens: 100}
	if got := usageFooterColor(u, diag); got != usageFooterOrange {
		t.Errorf("cache churn should use orange, got %q", got)
	}
}

func TestStatusBarShowsOpenCodeStyleModelCacheAndBrand(t *testing.T) {
	m := newTestChatTUI()
	m.width = 120
	m.label = "deepseek-pro"
	m.lastUsage = &provider.Usage{PromptTokens: 1000, CacheHitTokens: 670, CacheMissTokens: 330}

	got := m.renderStatusBar()
	for _, want := range []string{"deepseek-pro", "hit 67%", "DeepSeek-Reasonix", "Tab=plan"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status bar = %q, want %q", got, want)
		}
	}
}

func TestFormatContextTagLabelsContextUsage(t *testing.T) {
	got := formatContextTag(2_518, 1_000_000)
	if !strings.Contains(got, "ctx 2K / 1.0M (0%)") {
		t.Fatalf("context tag = %q, want explicit ctx label", got)
	}
}

func TestInputBoxShowsSessionTagWhenPresent(t *testing.T) {
	got := renderInputFrameTag("improve-reasonix-ui", 80)
	if !strings.Contains(got, "improve-reasonix-ui") {
		t.Fatalf("tag = %q, want branch label", got)
	}
	if visibleWidth(got) > 80 {
		t.Fatalf("tag width = %d, want <= 80: %q", visibleWidth(got), got)
	}
}

func TestChatInputUsesPromptWithoutTextareaBackground(t *testing.T) {
	m := newTestChatTUI()
	m.input.SetWidth(76)

	got := m.input.View()
	if !strings.Contains(got, "›") {
		t.Fatalf("input view = %q, want fixed prompt", got)
	}
	if strings.Contains(got, "\x1b[48;") {
		t.Fatalf("input view should not render textarea background blocks, got %q", got)
	}
}

func TestViewPinsBottomDockToTerminalBottom(t *testing.T) {
	m := newTestChatTUI()
	m.width = 80
	m.height = 12
	m.input.SetWidth(76)
	m.input.Focus()

	view := m.View()
	if got := lineCount(view.Content); got != m.height {
		t.Fatalf("bottom dock should be padded to terminal bottom, got height %d want %d\n%q", got, m.height, view.Content)
	}
	if !strings.HasPrefix(view.Content, "\n") {
		t.Fatalf("bottom dock should have top padding, got %q", view.Content)
	}
}

func TestViewDoesNotPreviewStreamingAnswerInNormalBuffer(t *testing.T) {
	m := newTestChatTUI()
	m.width = 80
	m.height = 12
	m.input.SetWidth(76)
	m.pending.WriteString("Visible answer should wait for scrollback commit")

	view := m.View()
	if strings.Contains(view.Content, i18n.M.LivePreviewLabel) || strings.Contains(view.Content, "Visible answer") {
		t.Fatalf("streaming answer should not render inside the bottom dock, got %q", view.Content)
	}
}

func TestTUIBannerRendersWelcomeCard(t *testing.T) {
	got := renderTUIBanner("deepseek-flash", "", 120)

	for _, want := range []string{
		"REASONIX",
		"DeepSeek",
		"DeepSeek-native coding agent",
		"cache-first",
		"Welcome back!",
		"Tips for getting started",
		"What's new",
		"/init",
		"/cache",
		"deepseek-flash",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("banner = %q, want %q", got, want)
		}
	}
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
