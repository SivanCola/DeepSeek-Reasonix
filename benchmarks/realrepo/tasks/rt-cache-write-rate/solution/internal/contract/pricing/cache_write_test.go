package pricing

import "testing"

func TestCacheWriteRateChargesWrittenTokens(t *testing.T) {
	card := RateCard{Input: 3, CacheWrite: 3.75, Currency: "USD"}
	usage := UsageTokens{CacheMissTokens: 1000000, CacheWriteTokens: 400000}
	if got := OriginalCostAmount(card, usage).String(); got != "3.3" {
		t.Fatalf("cost = %s, want 3.3", got)
	}
	card.CacheWrite = 0
	if got := OriginalCostAmount(card, usage).String(); got != "3" {
		t.Fatalf("cost without a cache-write rate = %s, want 3", got)
	}
}

func TestCacheWriteSeparatesFingerprintsAndCards(t *testing.T) {
	base := RateCard{CacheHit: 0.02, Input: 1, Output: 2, Currency: "CNY"}
	withWrite := base
	withWrite.CacheWrite = 1.25
	if PricingFingerprint(base) == PricingFingerprint(withWrite) {
		t.Fatal("cache-write rate does not reach the fingerprint")
	}
	if sameCard(base, withWrite) {
		t.Fatal("cards differing in CacheWrite compare equal")
	}
}

func TestAnthropicPricesCacheWrites(t *testing.T) {
	got := CurrentRate("anthropic", "claude-sonnet-4-5", "USD")
	if got == nil || got.CacheWrite != 3.75 {
		t.Fatalf("claude-sonnet-4-5 = %+v", got)
	}
	if OfficialProviderForEndpoint("https://api.anthropic.com/v1/messages") != "anthropic" {
		t.Fatal("api.anthropic.com is not recognised as Anthropic's endpoint")
	}
}
