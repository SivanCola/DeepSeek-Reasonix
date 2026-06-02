package main

import (
	"testing"

	"reasonix/internal/provider"
)

func TestHistoryMessagesIncludeAssistantReasoning(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "expanded prompt"},
		{Role: provider.RoleAssistant, Content: "answer", ReasoningContent: "thinking trace"},
		{Role: provider.RoleTool, Content: "tool output", ReasoningContent: "ignored by frontend filter"},
		{Role: provider.RoleAssistant, ReasoningContent: "tool-call-only thinking"},
	}

	got := historyMessages(msgs, func(content string) string {
		if content != "expanded prompt" {
			t.Fatalf("unexpected user content passed to resolver: %q", content)
		}
		return "display prompt"
	})

	if len(got) != len(msgs) {
		t.Fatalf("history length = %d, want %d", len(got), len(msgs))
	}
	if got[0].Content != "display prompt" {
		t.Fatalf("user display content = %q, want display prompt", got[0].Content)
	}
	if got[1].Reasoning != "thinking trace" {
		t.Fatalf("assistant reasoning = %q, want thinking trace", got[1].Reasoning)
	}
	if got[2].Reasoning != "" {
		t.Fatalf("non-assistant reasoning should stay hidden, got %q", got[2].Reasoning)
	}
	if got[3].Reasoning != "tool-call-only thinking" {
		t.Fatalf("empty-content assistant reasoning = %q, want tool-call-only thinking", got[3].Reasoning)
	}
}
