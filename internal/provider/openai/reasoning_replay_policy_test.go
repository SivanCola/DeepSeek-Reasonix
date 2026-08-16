package openai

import (
	"testing"

	"reasonix/internal/provider"
)

func TestGLMRequiresOnlyProviderIssuedReasoningReplay(t *testing.T) {
	p, err := New(provider.Config{
		Name: "glm", BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4", Model: "glm-5.2", APIKey: "k",
		Extra: map[string]any{"reasoning_protocol": "glm"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !provider.RequiresReasoningRoundTrip(p) {
		t.Fatal("thinking-enabled GLM must preserve provider-issued reasoning")
	}
	if !provider.RequiresAssistantReasoningReplay(p, provider.Message{Role: provider.RoleAssistant, ReasoningContent: "provider reasoning"}) {
		t.Fatal("GLM must replay reasoning the provider emitted")
	}
	if provider.RequiresAssistantReasoningReplay(p, provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call_1", Name: "read_file"}}}) {
		t.Fatal("GLM tool turns without provider reasoning must remain replayable")
	}
}
