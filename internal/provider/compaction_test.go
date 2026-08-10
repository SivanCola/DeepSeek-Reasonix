package provider_test

import (
	"context"
	"testing"

	"reasonix/internal/provider"
)

type capProvider struct{}

func (capProvider) Name() string { return "cap" }
func (capProvider) Stream(context.Context, provider.Request) (<-chan provider.Chunk, error) {
	return nil, nil
}
func (capProvider) CompactionCapabilities() provider.CompactionCapabilities {
	return provider.CompactionCapabilities{MaxOutputTokens: 8192, CacheTTL: 0}
}

func TestCompactionCapablerLookup(t *testing.T) {
	cc, ok := provider.AsCompactionCapabler(capProvider{})
	if !ok {
		t.Fatal("expected CompactionCapabler")
	}
	if got := cc.CompactionCapabilities().MaxOutputTokens; got != 8192 {
		t.Fatalf("MaxOutputTokens = %d", got)
	}
	if _, ok := provider.AsCompactionCapabler(nil); ok {
		t.Fatal("nil provider must not report capability")
	}
}
