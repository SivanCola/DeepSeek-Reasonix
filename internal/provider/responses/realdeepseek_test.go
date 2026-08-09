//go:build live

package responses

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// TestRealOpenCodeGoDeepSeekResponsesWebSearch exercises Reasonix's stateless
// Responses request, server-side web search, and replay-item capture against the
// OpenCode Go DeepSeek Flash route.
func TestRealOpenCodeGoDeepSeekResponsesWebSearch(t *testing.T) {
	key := os.Getenv("OPENCODE_GO_API_KEY")
	if key == "" {
		t.Skip("OPENCODE_GO_API_KEY not set — skipping live probe")
	}

	p := New(Config{
		Name:            "opencode-go-deepseek-responses",
		BaseURL:         "https://opencode.ai/zen/go/v1",
		Model:           "deepseek-v4-flash",
		APIKey:          key,
		KeyEnv:          "OPENCODE_GO_API_KEY",
		Effort:          "high",
		Mode:            "stateless",
		WebSearch:       true,
		MaxOutputTokens: 768,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	stream, err := p.Stream(ctx, provider.Request{Messages: []provider.Message{{
		Role: provider.RoleUser, Content: "Search the web for the OpenCode Go documentation and reply with one source URL.",
	}}, MaxTokens: 768})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var text strings.Builder
	searchItems := 0
	for chunk := range stream {
		switch chunk.Type {
		case provider.ChunkText:
			text.WriteString(chunk.Text)
		case provider.ChunkResponsesItem:
			searchItems++
		case provider.ChunkError:
			t.Fatalf("stream error: %v", chunk.Err)
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		t.Fatal("OpenCode Go Responses web_search returned no assistant text")
	}
	if searchItems == 0 {
		t.Fatal("OpenCode Go Responses returned no completed web_search_call replay item")
	}
	t.Logf("opencode-go-deepseek-responses web_search: text=%d search_items=%d", len(text.String()), searchItems)
}

// TestRealDeepSeekResponsesWebSearch exercises the official stateless
// Responses endpoint with its provider-executed web_search tool. It is
// credential-gated and build-tagged so ordinary CI remains deterministic.
func TestRealDeepSeekResponsesWebSearch(t *testing.T) {
	key := os.Getenv("DEEPSEEK_API_KEY")
	if key == "" {
		t.Skip("DEEPSEEK_API_KEY not set — skipping live probe")
	}

	p := New(Config{
		Name:            "deepseek-responses",
		BaseURL:         "https://api.deepseek.com",
		Model:           "deepseek-v4-flash",
		APIKey:          key,
		Effort:          "disabled",
		WebSearch:       true,
		MaxOutputTokens: 256,
		KeyEnv:          "DEEPSEEK_API_KEY",
	})
	chunks := collect(t, p, provider.Request{Messages: []provider.Message{{
		Role:    provider.RoleUser,
		Content: "Search the web for the latest DeepSeek API documentation update and reply with one source URL.",
	}}, MaxTokens: 256})
	var text strings.Builder
	for _, chunk := range chunks {
		if chunk.Type == provider.ChunkText {
			text.WriteString(chunk.Text)
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		t.Fatalf("web_search returned no assistant text")
	}
	t.Logf("web_search: text=%d chunks=%d", len(text.String()), len(chunks))
}
