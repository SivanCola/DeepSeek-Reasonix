package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/base/secrets/secretstest"
)

// A secret the host holds for the person never reaches a model in any part of
// a request it rides in, and the conversation the host keeps is left as it was.
func TestMaskOutboundMasksEveryPartAModelReads(t *testing.T) {
	secretstest.Hold("SHOP_KEY", "sk-outbound-0123456789")
	in := Request{Messages: []Message{
		{Role: RoleSystem, Content: "remember sk-outbound-0123456789"},
		{Role: RoleUser, Content: "my key is sk-outbound-0123456789", ProviderContent: "key sk-outbound-0123456789"},
		{Role: RoleAssistant, ReasoningContent: "the key sk-outbound-0123456789", ToolCalls: []ToolCall{{ID: "c1", Name: "bash", Arguments: `{"command":"echo sk-outbound-0123456789"}`}}},
		{Role: RoleTool, ToolCallID: "c1", Content: "sk-outbound-0123456789"},
		{Role: RoleAssistant, ResponsesItems: []json.RawMessage{json.RawMessage(`{"type":"message","content":[{"type":"output_text","text":"sk-outbound-0123456789"}]}`)}},
	}}
	out := MaskOutbound(in)
	data, _ := json.Marshal(out)
	if strings.Contains(string(data), "sk-outbound") || strings.Count(string(data), "[secret:SHOP_KEY]") != 7 {
		t.Fatalf("outbound request = %s", data)
	}
	kept, _ := json.Marshal(in)
	if strings.Count(string(kept), "sk-outbound-0123456789") != 7 {
		t.Fatalf("masking rewrote the host's own copy: %s", kept)
	}
}

// Bytes the provider signed are its own, and altering them fails every later
// request; they are sent as they came back.
func TestMaskOutboundLeavesSignedBytesWhole(t *testing.T) {
	secretstest.Hold("SIGNED", "sk-signed-0123456789")
	in := Request{Messages: []Message{
		{Role: RoleAssistant, ReasoningContent: "sk-signed-0123456789", ReasoningSignature: "sig",
			ToolCalls:      []ToolCall{{ID: "c1", Name: "bash", Arguments: `{"x":"sk-signed-0123456789"}`, ThoughtSignature: "ts"}},
			ResponsesItems: []json.RawMessage{json.RawMessage(`{"type":"reasoning","summary":[{"text":"sk-signed-0123456789"}]}`)}},
	}}
	out := MaskOutbound(in).Messages[0]
	if out.ReasoningContent != in.Messages[0].ReasoningContent || out.ToolCalls[0].Arguments != in.Messages[0].ToolCalls[0].Arguments || string(out.ResponsesItems[0]) != string(in.Messages[0].ResponsesItems[0]) {
		t.Fatalf("signed bytes were rewritten: %+v", out)
	}
}

// The stored projection compaction writes back is the conversation as it was:
// masking happens on the wire, never in what the host keeps.
func TestProjectionsDoNotMask(t *testing.T) {
	secretstest.Hold("KEPT", "sk-kept-0123456789")
	msgs := []Message{{Role: RoleUser, Content: "sk-kept-0123456789"}}
	if got := ProjectionMessages(msgs); got[0].Content != "sk-kept-0123456789" {
		t.Fatalf("projection = %q", got[0].Content)
	}
	if got := ModelMessages(msgs); got[0].Content != "sk-kept-0123456789" {
		t.Fatalf("model messages = %q", got[0].Content)
	}
}
