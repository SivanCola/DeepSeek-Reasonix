package cli

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"

	"reasonix/internal/event"
)

// newTestChatTUI builds a chatTUI with just the pieces the streaming/commit and
// completion paths need, for unit tests that don't run the bubbletea loop.
func newTestChatTUI() chatTUI {
	commit := []string{}
	ti := textarea.New()
	configureChatTextarea(&ti)
	ti.SetWidth(80)
	return chatTUI{
		input:           ti,
		reasoning:       &strings.Builder{},
		pending:         &strings.Builder{},
		pendingCommit:   &commit,
		turnAccumulator: &strings.Builder{},
		renderer:        newMarkdownRenderer(80),
	}
}

// TestIngestHidesReasoningAndKeepsAnswerLive proves the TUI consumes reasoning
// without committing it to scrollback, while the visible answer remains live
// until it is flushed.
func TestIngestHidesReasoningAndKeepsAnswerLive(t *testing.T) {
	m := newTestChatTUI()

	m.ingestEvent(event.Event{Kind: event.Reasoning, Text: "...reasoning..."})
	if len(*m.pendingCommit) != 0 {
		t.Fatalf("reasoning should not commit, committed=%v", *m.pendingCommit)
	}

	m.ingestEvent(event.Event{Kind: event.Text, Text: "Hello answer"})
	if n := len(*m.pendingCommit); n != 0 {
		t.Fatalf("reasoning should stay hidden when the answer begins, committed=%v", *m.pendingCommit)
	}
	if m.pending.String() != "Hello answer" {
		t.Errorf("answer should be live in pending, got %q", m.pending.String())
	}
	if m.reasoning.Len() != 0 {
		t.Errorf("reasoning buffer should be cleared after commit")
	}

	m.commitPending() // turn end
	if n := len(*m.pendingCommit); n != 1 || !strings.Contains((*m.pendingCommit)[0], "Hello") {
		t.Fatalf("answer should commit on flush, committed=%v", *m.pendingCommit)
	}
}

func TestIngestHidesReasoningByDefault(t *testing.T) {
	m := newTestChatTUI()

	m.ingestEvent(event.Event{Kind: event.Reasoning, Text: "internal chain of thought"})
	m.ingestEvent(event.Event{Kind: event.Text, Text: "Visible answer"})
	m.commitPending()

	joined := strings.Join(*m.pendingCommit, "\n")
	if strings.Contains(joined, "internal chain of thought") || strings.Contains(joined, "thinking") {
		t.Fatalf("reasoning should be hidden from TUI scrollback, got %q", joined)
	}
	if !strings.Contains(joined, "Visible answer") {
		t.Fatalf("visible answer should still render, got %q", joined)
	}
}

// TestIngestEventFlushesAnswer confirms an event line (e.g. a tool dispatch)
// finalizes the answer streamed before it, preserving order in scrollback.
func TestIngestEventFlushesAnswer(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.Text, Text: "partial answer "})
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{Name: "read_file", Args: `{"path":"x"}`}})
	if n := len(*m.pendingCommit); n != 2 {
		t.Fatalf("answer then event line should be two commits, got %d: %v", n, *m.pendingCommit)
	}
	if !strings.Contains((*m.pendingCommit)[0], "partial answer") {
		t.Errorf("first commit should be the buffered answer, got %q", (*m.pendingCommit)[0])
	}
	if !strings.Contains((*m.pendingCommit)[1], "-> read_file") {
		t.Errorf("second commit should be the event line, got %q", (*m.pendingCommit)[1])
	}
	if m.pending.Len() != 0 {
		t.Errorf("answer buffer should be drained after the event line")
	}
}
