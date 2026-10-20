#!/usr/bin/env bash
set -e
export GOFLAGS=-mod=mod GOTOOLCHAIN=local
cat > internal/contract/pricing/zz_grader_test.go <<'GO'
package pricing

import (
	"testing"
	"time"
)

func TestGraderCacheWriteCost(t *testing.T) {
	card := RateCard{CacheHit: 0.3, Input: 3, Output: 15, Currency: "USD"}
	priced := card
	priced.CacheWrite = 3.75
	cases := []struct {
		name  string
		card  RateCard
		usage UsageTokens
		want  string
	}{
		{"no write, no rate", card, UsageTokens{CacheHitTokens: 200000, CacheMissTokens: 800000, CompletionTokens: 50000}, "3.21"},
		{"no write, rate set", priced, UsageTokens{CacheHitTokens: 200000, CacheMissTokens: 800000, CompletionTokens: 50000}, "3.21"},
		{"write at input", card, UsageTokens{CacheMissTokens: 1000000, CacheWriteTokens: 400000, CompletionTokens: 100000}, "4.5"},
		{"write at cache-write", priced, UsageTokens{CacheMissTokens: 1000000, CacheWriteTokens: 400000, CompletionTokens: 100000}, "4.8"},
		{"billed units at input", card, UsageTokens{CacheMissTokens: 1000000, CacheWriteTokens: 400000, CacheWriteBilledTokens: 500000}, "3.3"},
		{"billed units at cache-write", priced, UsageTokens{CacheMissTokens: 1000000, CacheWriteTokens: 400000, CacheWriteBilledTokens: 500000}, "3.675"},
	}
	for _, tc := range cases {
		if got := OriginalCostAmount(tc.card, tc.usage).String(); got != tc.want {
			t.Errorf("%s: cost = %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestGraderFingerprintKeepsStoredKeys(t *testing.T) {
	cny := RateCard{CacheHit: 0.02, Input: 1, Output: 4, Currency: "CNY"}
	usd := RateCard{CacheHit: 0.3, Input: 3, Output: 15, Currency: "USD"}
	if got := PricingFingerprint(cny); got != "ba96037de26c3f79" {
		t.Errorf("fingerprint of a card without CacheWrite moved: %s", got)
	}
	if got := PricingFingerprint(usd); got != "e16b53b4599e5fda" {
		t.Errorf("fingerprint of a card without CacheWrite moved: %s", got)
	}
	withWrite := usd
	withWrite.CacheWrite = 3.75
	if PricingFingerprint(withWrite) == PricingFingerprint(usd) {
		t.Error("cards differing only in CacheWrite share a fingerprint")
	}
	other := usd
	other.CacheWrite = 6
	if PricingFingerprint(withWrite) == PricingFingerprint(other) {
		t.Error("two different CacheWrite rates share a fingerprint")
	}
}

func TestGraderCacheWriteIsPartOfCardIdentity(t *testing.T) {
	official := RateCard{CacheHit: 0.02, Input: 1, Output: 2, Currency: "CNY"}
	if !IsOfficialCard("mimo", "mimo-v2.5", official) {
		t.Fatal("the official mimo-v2.5 card is no longer recognised")
	}
	changed := official
	changed.CacheWrite = 1.25
	if IsOfficialCard("mimo", "mimo-v2.5", changed) {
		t.Error("a card with a cache-write rate the vendor never published is recognised as official")
	}
	if _, ok := MatchesCatalog("mimo", "mimo-v2.5", official); !ok {
		t.Fatal("the official mimo-v2.5 card no longer matches the catalog")
	}
	if _, ok := MatchesCatalog("mimo", "mimo-v2.5", changed); ok {
		t.Error("a card with an unpublished cache-write rate matches the catalog")
	}
}

func TestGraderAnthropicIsPriced(t *testing.T) {
	want := RateCard{CacheHit: 0.30, Input: 3, Output: 15, CacheWrite: 3.75, Currency: "USD"}
	got := CurrentRate("anthropic", "claude-sonnet-4-5", "USD")
	if got == nil || *got != want {
		t.Fatalf("CurrentRate(anthropic, claude-sonnet-4-5) = %+v, want %+v", got, want)
	}
	if !IsOfficialCard("anthropic", "claude-sonnet-4-5", want) {
		t.Error("the Anthropic card is not recognised as official")
	}
	e, ok := LookupCatalog("anthropic", "claude-sonnet-4-5", "USD", "")
	if !ok || e.DocURL != "https://www.anthropic.com/pricing" || e.CheckedOn != "2026-10-19" || e.CacheWrite != 3.75 {
		t.Errorf("catalog entry = %+v, %v", e, ok)
	}
	if got := OfficialProviderForEndpoint("https://api.anthropic.com/v1/messages"); got != "anthropic" {
		t.Errorf("api.anthropic.com resolves to %q", got)
	}
	if got := DefaultCurrency("anthropic"); got != "USD" {
		t.Errorf("DefaultCurrency(anthropic) = %q", got)
	}
}

func TestGraderCatalogCarriesCacheWrite(t *testing.T) {
	e := CatalogEntry{Provider: "x", Model: "y", Currency: "USD", CacheHit: 0.1, Input: 1, Output: 2, CacheWrite: 1.25}
	if got := RateCardFromCatalog(e); got.CacheWrite != 1.25 || got.Input != 1 {
		t.Errorf("RateCardFromCatalog = %+v", got)
	}
	if got := e.RatesAt(time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)); got.CacheWrite != 1.25 {
		t.Errorf("RatesAt = %+v", got)
	}
}
GO
gofmt_out="$(gofmt -l .)"
[ -z "$gofmt_out" ] || { echo "gofmt needed: $gofmt_out"; exit 1; }
go vet ./...
go test ./... >/dev/null
out="$(go run ./cmd/benchctl cost -provider mimo -model mimo-v2.5 -miss 1000000 -write 400000)"
[ "$out" = "1 CNY" ] || { echo "benchctl cost -write: got $out"; exit 1; }
out="$(go run ./cmd/benchctl rate -provider anthropic -model claude-sonnet-4-5)"
case "$out" in *"cache-write 3.75"*) ;; *) echo "benchctl rate does not show the cache-write rate: $out"; exit 1 ;; esac
out="$(go run ./cmd/benchctl rate -provider mimo -model mimo-v2.5)"
case "$out" in *"cache-write"*) echo "benchctl rate shows a cache-write rate the card does not have: $out"; exit 1 ;; esac
grep -lE 'CacheWrite:' internal/contract/pricing/*_test.go | grep -qv zz_grader_test.go \
	|| { echo "no test of the pricing package sets a CacheWrite rate"; exit 1; }
awk '/^## Pricing/{on=1; next} /^## /{on=0} on' README.md | grep -q 'CacheWrite' \
	|| { echo "README's pricing section does not mention CacheWrite"; exit 1; }
