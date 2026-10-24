package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"reasonix/internal/contract/provider"
)

type scriptedTitleProvider struct {
	stream func(ctx context.Context) (<-chan provider.Chunk, error)
}

func (scriptedTitleProvider) Name() string { return "scripted-title" }
func (p scriptedTitleProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	return p.stream(ctx)
}

func chunks(cs ...provider.Chunk) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, len(cs))
	for _, c := range cs {
		ch <- c
	}
	close(ch)
	return ch, nil
}

// Automatic naming that fails says why: a refusal by the endpoint carries its
// status and words, a silent model and a slow one are told apart from each other.
func TestAutoNameFailureNamesItsCause(t *testing.T) {
	cases := []struct {
		name   string
		stream func(ctx context.Context) (<-chan provider.Chunk, error)
		code   string
		status int
		detail string
	}{
		{"endpoint refuses", func(context.Context) (<-chan provider.Chunk, error) {
			return nil, &provider.APIError{Provider: "p", Status: 404, Body: `{"error":"model step-5-preview not found"}`}
		}, "session.title_refused", http.StatusBadGateway, "model step-5-preview not found"},
		{"model answers nothing", func(context.Context) (<-chan provider.Chunk, error) {
			return chunks(provider.Chunk{Type: provider.ChunkText, Text: "  "}, provider.Chunk{Type: provider.ChunkDone})
		}, "session.title_no_text", http.StatusBadGateway, ""},
		{"model spends the budget thinking", func(context.Context) (<-chan provider.Chunk, error) {
			return chunks(provider.Chunk{Type: provider.ChunkReasoning, Text: "let me think about a title"}, provider.Chunk{Type: provider.ChunkDone})
		}, "session.title_thought_only", http.StatusBadGateway, ""},
		{"model reports reasoning tokens and no text", func(context.Context) (<-chan provider.Chunk, error) {
			return chunks(provider.Chunk{Type: provider.ChunkUsage, Usage: &provider.Usage{CompletionTokens: 60, ReasoningTokens: 60}}, provider.Chunk{Type: provider.ChunkDone})
		}, "session.title_thought_only", http.StatusBadGateway, ""},
		{"model too slow", func(ctx context.Context) (<-chan provider.Chunk, error) {
			ch := make(chan provider.Chunk)
			go func() { <-ctx.Done(); close(ch) }()
			return ch, nil
		}, "session.title_timeout", http.StatusGatewayTimeout, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, rt, path, _ := titleSession(t)
			rt.Server.titleModel.prov = scriptedTitleProvider{stream: tc.stream}
			old := titleFillTimeout
			titleFillTimeout = 200 * time.Millisecond
			t.Cleanup(func() { titleFillTimeout = old })
			res := titlePost(t, h, "auto-name", path, "")
			var reason Reason
			if err := json.Unmarshal(res.Body.Bytes(), &reason); err != nil {
				t.Fatalf("body %q: %v", res.Body.String(), err)
			}
			if res.Code != tc.status || reason.Code != tc.code {
				t.Fatalf("got %d %q, want %d %q (%s)", res.Code, reason.Code, tc.status, tc.code, res.Body.String())
			}
			if detail, _ := reason.Params["detail"].(string); !strings.Contains(detail, tc.detail) {
				t.Fatalf("detail %q does not carry %q", detail, tc.detail)
			}
		})
	}
}
