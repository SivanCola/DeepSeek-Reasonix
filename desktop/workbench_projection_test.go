package main

import (
	"testing"

	"reasonix/internal/eventwire"
	"reasonix/internal/remote/protocol"
)

func strptr(value string) *string { return &value }

func TestWorkbenchHistoryProjectionPreservesReasoningToolsAndCitations(t *testing.T) {
	page := workbenchHistoryPage(protocol.HistoryPage{
		Messages: []protocol.HistoryMessage{{
			Role: "assistant", Content: strptr("done"), Reasoning: strptr("thought"),
			MemoryCitations: []eventwire.MemoryCitation{{ID: "m1", Source: "MEMORY.md", LineStart: 2, LineEnd: 3}},
			ToolCalls:       []protocol.HistoryToolCall{{ID: "tc1", Name: "read_file", Arguments: strptr(`{"path":"a"}`), Summary: strptr("read a")}},
		}},
		StartTurn: 1, EndTurn: 1, TotalTurns: 1,
	})
	if len(page.Messages) != 1 || page.Messages[0].Content != "done" || page.Messages[0].Reasoning != "thought" {
		t.Fatalf("history projection lost message fields: %+v", page)
	}
	if got := page.Messages[0].ToolCalls; len(got) != 1 || got[0].Arguments != `{"path":"a"}` {
		t.Fatalf("history projection lost tool call: %+v", got)
	}
	if got := page.Messages[0].MemoryCitations; len(got) != 1 || got[0].ID != "m1" || got[0].LineEnd != 3 {
		t.Fatalf("history projection lost citation: %+v", got)
	}
}

func TestWorkbenchSnapshotProjectionUsesRemoteWorkspaceAndProfile(t *testing.T) {
	snapshot := protocol.SessionSnapshot{Meta: protocol.SessionMetaSnapshot{
		Title: "Remote", ResolvedProfile: protocol.ResolvedProfile{
			Model: "deepseek/deepseek-chat", Effort: "high", CollaborationMode: protocol.CollaborationNormal,
			TokenMode: protocol.TokenFull, ToolApprovalMode: protocol.ToolApprovalAuto,
		},
	}}
	meta := workbenchMeta(snapshot, "/srv/app")
	if meta.WorkspaceRoot != "/srv/app" || meta.Label != "deepseek/deepseek-chat" || !meta.AutoApproveTools || meta.Bypass {
		t.Fatalf("meta projection = %+v", meta)
	}
}

func TestWorkbenchContextProjectionPreservesAllSourceTotals(t *testing.T) {
	context := workbenchContextInfo(protocol.ContextView{
		UsedTokens: 10, WindowTokens: 100, TotalTokens: 30, SessionCacheHitTokens: 7, SessionCacheMissTokens: 2,
		Sources: []protocol.UsageSourceView{{Source: "executor", TotalTokens: 30, RequestCount: 2}},
	})
	if context.Used != 10 || context.Window != 100 || context.SessionTokens != 30 || context.CacheHitTokens != 7 {
		t.Fatalf("context projection = %+v", context)
	}
	if context.Sources["executor"].RequestCount != 2 {
		t.Fatalf("sources = %+v", context.Sources)
	}
}
