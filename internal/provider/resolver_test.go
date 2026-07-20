package provider

import (
	"context"
	"testing"
)

type stubProvider struct{ name string }

func (s stubProvider) Name() string { return s.name }
func (s stubProvider) Stream(context.Context, Request) (<-chan Chunk, error) {
	ch := make(chan Chunk)
	close(ch)
	return ch, nil
}

func TestStaticResolverCatalogAndResolve(t *testing.T) {
	r := &StaticResolver{
		Descriptors: []Descriptor{{Ref: "deepseek/chat", DisplayName: "DeepSeek", Model: "chat"}},
		Providers:   map[string]Provider{"deepseek/chat": stubProvider{name: "deepseek"}},
	}
	if got := len(r.Catalog()); got != 1 {
		t.Fatalf("catalog len = %d", got)
	}
	p, err := r.Resolve(Selection{Ref: "deepseek/chat"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "deepseek" {
		t.Fatalf("name = %q", p.Name())
	}
	if _, err := r.Resolve(Selection{Ref: "missing"}); err == nil {
		t.Fatal("expected missing ref error")
	}
}
