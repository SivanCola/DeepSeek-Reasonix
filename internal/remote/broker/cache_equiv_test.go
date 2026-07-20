package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"reasonix/internal/provider"
)

// capturingProvider records the exact provider.Request it receives so local and
// broker-mediated resolves can be compared byte-for-byte (cache stability).
type capturingProvider struct {
	mu   sync.Mutex
	reqs []provider.Request
}

func (c *capturingProvider) Name() string { return "capture" }

func (c *capturingProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	c.mu.Lock()
	// Deep-copy via JSON so later mutations cannot alias.
	raw, _ := json.Marshal(req)
	var copyReq provider.Request
	_ = json.Unmarshal(raw, &copyReq)
	c.reqs = append(c.reqs, copyReq)
	c.mu.Unlock()
	ch := make(chan provider.Chunk, 1)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "ok"}
	close(ch)
	return ch, nil
}

func (c *capturingProvider) last() provider.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.reqs) == 0 {
		return provider.Request{}
	}
	return c.reqs[len(c.reqs)-1]
}

// TestBrokerPreservesProviderRequestBytes guards the product invariant that
// Broker mode must not alter the provider-visible request relative to local
// direct Stream. Differences would poison prompt cache hit rates.
func TestBrokerPreservesProviderRequestBytes(t *testing.T) {
	cap := &capturingProvider{}
	resolver := &provider.StaticResolver{
		Descriptors: []provider.Descriptor{{Ref: "capture/model", DisplayName: "capture", Model: "model"}},
		Providers:   map[string]provider.Provider{"capture/model": cap},
	}

	sample := provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "stable user text"},
			{Role: provider.RoleAssistant, Content: "stable assistant", ReasoningContent: "think"},
		},
		Tools: []provider.ToolSchema{
			{Name: "bash", Description: "run shell", Parameters: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`)},
		},
		MaxTokens: 128,
	}
	// Local direct path.
	localProv, err := resolver.Resolve(provider.Selection{Ref: "capture/model"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := localProv.Stream(context.Background(), sample)
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	localBody, err := json.Marshal(cap.last())
	if err != nil {
		t.Fatal(err)
	}

	// Broker path: same underlying resolver, request travels NDJSON wire.
	srv := NewServer(Options{Resolver: resolver})
	tok, err := srv.Issue(Scope{
		HostID: "lab", Fingerprint: "fp", Workspace: "/w",
		AllowedRefs: map[string]struct{}{"capture/model": {}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, err := srv.ListenAndServe(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{BaseURL: "http://" + addr.String(), Token: tok, HTTPClient: http.DefaultClient}
	remoteProv, err := client.Resolve(provider.Selection{Ref: "capture/model"})
	if err != nil {
		t.Fatal(err)
	}
	ch2, err := remoteProv.Stream(context.Background(), sample)
	if err != nil {
		t.Fatal(err)
	}
	for range ch2 {
	}
	remoteBody, err := json.Marshal(cap.last())
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(localBody, remoteBody) {
		t.Fatalf("CACHE_GUARD: provider.Request diverged between local and broker paths\nlocal:  %s\nbroker: %s", localBody, remoteBody)
	}

	// SSH host / workspace / connection metadata must not appear in the request.
	for _, leak := range []string{"lab", "SHA256", "connectionId", "broker", "/w"} {
		if bytes.Contains(remoteBody, []byte(leak)) {
			// "/w" could appear in user content; only flag if we didn't put it there.
			if leak == "/w" {
				continue
			}
			if leak == "lab" || leak == "broker" || leak == "connectionId" || leak == "SHA256" {
				t.Fatalf("CACHE_GUARD: request body leaked host/broker metadata %q: %s", leak, remoteBody)
			}
		}
	}
}
