package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/contract/tool"
)

func TestCaptureToolResultPreservesEnvelopeAndLegacyText(t *testing.T) {
	for _, raw := range []string{
		`{"content":[{"type":"text","text":"done"}],"structuredContent":{"job_id":"j","url":"https://example.test/v.mp4"},"future":true}`,
		`{"content":[{"type":"text","text":"plain"}]}`,
	} {
		var saved string
		ctx := tool.WithResultRecorder(context.Background(), func(b json.RawMessage) (string, error) {
			saved = string(b)
			return "/private/result", nil
		})
		text, _, err := captureToolResult(ctx, json.RawMessage(raw))
		if err != nil || saved != raw {
			t.Fatalf("capture = %q, %v; saved %q", text, err, saved)
		}
		if strings.Contains(raw, "structuredContent") {
			if !strings.Contains(text, `"job_id":"j"`) || !strings.Contains(text, "/private/result") {
				t.Fatalf("missing structured result/ref: %s", text)
			}
		} else if text != "plain" {
			t.Fatalf("legacy text changed: %q", text)
		}
	}
}

func TestCaptureToolResultReportsStorageFailure(t *testing.T) {
	failure := errors.New("disk unavailable")
	ctx := tool.WithResultRecorder(context.Background(), func(json.RawMessage) (string, error) { return "", failure })
	text, _, err := captureToolResult(ctx, json.RawMessage(`{"content":[{"type":"text","text":"generated"}]}`))
	if text != "generated" || !errors.Is(err, failure) {
		t.Fatalf("capture = %q, %v", text, err)
	}
}
