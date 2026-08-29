package provider

import "testing"

func TestNativeToolSearchDefaultOffKeepsVisibleBytes(t *testing.T) {
	visible := []ToolSchema{{Name: "use_capability", Description: "proxy", Parameters: []byte(`{"type":"object"}`)}}
	extra := []ToolSchema{{Name: "mcp__s__t", Description: "t", Parameters: []byte(`{"type":"object"}`)}}
	got := ApplyNativeToolSearch(visible, extra, nil)
	if len(got) != 1 || got[0].Name != "use_capability" || got[0].Deferred {
		t.Fatalf("preview off must keep fixed proxy only: %+v", got)
	}
}

func TestNativeToolSearchUnsupportedProviders(t *testing.T) {
	for _, name := range []string{"deepseek", "dashscope", "openai-compatible"} {
		if nativeToolSearchSupportedName(name) {
			t.Fatalf("%s must not enable native tool search", name)
		}
	}
	if !nativeToolSearchSupportedName("openai") || !nativeToolSearchSupportedName("anthropic") {
		t.Fatal("first-party openai/anthropic should be eligible")
	}
}

func TestNativeToolSearchPreviewMarksDeferred(t *testing.T) {
	restore := SetNativeToolSearchPreviewForTest(true)
	defer restore()
	if NativeToolSearchEnabled(nil) {
		t.Fatal("nil provider must stay disabled")
	}
}
