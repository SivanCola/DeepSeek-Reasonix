package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"reasonix/internal/provider"
)

type stubProv struct{ name string }

func (s stubProv) Name() string { return s.name }
func (s stubProv) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "hi"}
	ch <- provider.Chunk{Type: provider.ChunkUsage, Usage: &provider.Usage{PromptTokens: 1, CompletionTokens: 1}}
	close(ch)
	return ch, nil
}

func TestBrokerTokenScopeAndCatalog(t *testing.T) {
	resolver := &provider.StaticResolver{
		Descriptors: []provider.Descriptor{
			{Ref: "deepseek/chat", DisplayName: "DeepSeek", Model: "chat"},
			{Ref: "other/x", DisplayName: "Other", Model: "x"},
		},
		Providers: map[string]provider.Provider{
			"deepseek/chat": stubProv{name: "deepseek"},
			"other/x":       stubProv{name: "other"},
		},
	}
	srv := NewServer(Options{Resolver: resolver})
	tok, err := srv.Issue(Scope{
		HostID:      "lab",
		Fingerprint: "SHA256:abc",
		Workspace:   "/home/u/proj",
		AllowedRefs: map[string]struct{}{"deepseek/chat": {}},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, APIPrefix+"/catalog", nil)
	req.Header.Set("X-Reasonix-Broker-Token", tok.String())
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	var cat catalogResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &cat); err != nil {
		t.Fatal(err)
	}
	if len(cat.Providers) != 1 || cat.Providers[0].Ref != "deepseek/chat" {
		t.Fatalf("catalog = %+v", cat.Providers)
	}

	body, _ := json.Marshal(streamRequest{ProviderRef: "other/x", Request: provider.Request{}})
	req = httptest.NewRequest(http.MethodPost, APIPrefix+"/stream", bytes.NewReader(body))
	req.Header.Set("X-Reasonix-Broker-Token", tok.String())
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:1234"
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("denied status %d", rr.Code)
	}

	srv.Revoke(tok)
	req = httptest.NewRequest(http.MethodGet, APIPrefix+"/catalog", nil)
	req.Header.Set("X-Reasonix-Broker-Token", tok.String())
	req.RemoteAddr = "127.0.0.1:1234"
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("revoked status %d", rr.Code)
	}
}

func TestTrustStoreAuthorizeAndFingerprintChange(t *testing.T) {
	dir := t.TempDir()
	store := TrustStore{Dir: dir}
	if err := store.AuthorizeAll("lab", "ssh-ed25519", "SHA256:aaa", []string{"deepseek/chat"}); err != nil {
		t.Fatal(err)
	}
	if !store.Allows("lab", "SHA256:aaa", "deepseek/chat") {
		t.Fatal("expected allow")
	}
	if miss := store.MissingRefs("lab", "SHA256:aaa", []string{"deepseek/chat", "openai/gpt"}); len(miss) != 1 || miss[0] != "openai/gpt" {
		t.Fatalf("missing = %v", miss)
	}
	if store.Allows("lab", "SHA256:bbb", "deepseek/chat") {
		t.Fatal("changed fingerprint must not inherit trust")
	}
}

func TestBrokerRemoteProviderPropagatesToolCallReasoning(t *testing.T) {
	resolver := &provider.StaticResolver{
		Descriptors: []provider.Descriptor{{
			Ref: "deepseek/chat", ToolCallReasoning: true,
		}},
		Providers: map[string]provider.Provider{"deepseek/chat": stubProv{name: "deepseek"}},
	}
	srv := NewServer(Options{Resolver: resolver})
	tok, err := srv.Issue(Scope{
		HostID: "lab", Fingerprint: "fp", Workspace: "/w",
		AllowedRefs: map[string]struct{}{"deepseek/chat": {}},
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
	client := &Client{BaseURL: "http://" + addr.String(), Token: tok}
	p, err := client.Resolve(provider.Selection{Ref: "deepseek/chat"})
	if err != nil {
		t.Fatal(err)
	}
	if !provider.RequiresToolCallReasoning(p) {
		t.Fatal("broker-backed provider must advertise RequiresToolCallReasoning from catalog")
	}
}

func TestStreamAllowedProvider(t *testing.T) {
	resolver := &provider.StaticResolver{
		Descriptors: []provider.Descriptor{{Ref: "deepseek/chat"}},
		Providers:   map[string]provider.Provider{"deepseek/chat": stubProv{name: "deepseek"}},
	}
	srv := NewServer(Options{Resolver: resolver})
	tok, err := srv.Issue(Scope{
		HostID: "lab", Fingerprint: "fp", Workspace: "/w",
		AllowedRefs: map[string]struct{}{"deepseek/chat": {}},
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
	client := &Client{BaseURL: "http://" + addr.String(), Token: tok}
	p, err := client.Resolve(provider.Selection{Ref: "deepseek/chat"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Stream(context.Background(), provider.Request{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for c := range ch {
		if c.Type == provider.ChunkText {
			texts = append(texts, c.Text)
		}
	}
	if len(texts) != 1 || texts[0] != "hi" {
		t.Fatalf("texts = %v", texts)
	}
	time.Sleep(20 * time.Millisecond)
}
