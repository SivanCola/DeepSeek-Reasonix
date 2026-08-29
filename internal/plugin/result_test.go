package plugin

import (
	"strings"
	"testing"
)

func TestParseToolResultStructuredContentVariants(t *testing.T) {
	text, _, err := parseToolResult([]byte(`{"content":[],"structuredContent":{"ok":true}}`))
	if err != nil || text != `{"ok":true}` {
		t.Fatalf("empty text used structuredContent: %q %v", text, err)
	}
	text, _, err = parseToolResult([]byte(`{"content":[{"type":"text","text":"{\"ok\":true}"}],"structuredContent":{"ok":true}}`))
	if err != nil || text != `{"ok":true}` {
		t.Fatalf("equal JSON must collapse: %q %v", text, err)
	}
	text, _, err = parseToolResult([]byte(`{"content":[{"type":"text","text":"hello"}],"structuredContent":{"ok":true}}`))
	if err != nil || !strings.Contains(text, "hello") || !strings.Contains(text, "structuredContent") {
		t.Fatalf("unequal content must keep both: %q %v", text, err)
	}
	text, _, err = parseToolResult([]byte(`{"content":[{"type":"text","text":"boom"}],"isError":true}`))
	if err == nil || text != "boom" {
		t.Fatalf("error result: %q %v", text, err)
	}
}
