package agent

import (
	"context"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func TestStreamScrubsOnlyStaleReasoningBeforeRequest(t *testing.T) {
	prov := &mockProvider{name: "p", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "ok"},
		{Type: provider.ChunkDone},
	}}
	sess := &Session{Messages: []provider.Message{
		{Role: provider.RoleUser, Content: "first"},
		{Role: provider.RoleAssistant, Content: "plain", ReasoningContent: "stale"},
		{Role: provider.RoleAssistant, Content: "", ReasoningContent: "keep", ToolCalls: []provider.ToolCall{
			{ID: "call_1", Name: "lookup", Arguments: `{"q":"x"}`},
		}},
		{Role: provider.RoleTool, Content: "result", ToolCallID: "call_1", Name: "lookup"},
		{Role: provider.RoleUser, Content: "next"},
	}}
	a := New(prov, tool.NewRegistry(), sess, Options{}, event.Discard)

	if err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := prov.lastReq.Messages
	if got[1].ReasoningContent != "" {
		t.Fatalf("plain assistant reasoning was sent: %+v", got[1])
	}
	if got[2].ReasoningContent != "keep" {
		t.Fatalf("tool-call assistant reasoning = %q, want keep", got[2].ReasoningContent)
	}
	if sess.Messages[1].ReasoningContent != "stale" {
		t.Fatalf("session message was mutated: %+v", sess.Messages[1])
	}
}
