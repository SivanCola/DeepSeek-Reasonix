package intent

import (
	"context"
	"sync"
	"time"
)

// Classifier reads a user turn and produces a structured intent. It is the only
// tier permitted to interpret prose; the cheaper tiers read explicit syntax,
// host toggles, or the model's own typed output.
//
// A Classifier that cannot answer must return KindUnknown with a nil error
// rather than guessing. Callers distinguish "read as chat" from "could not
// read" and apply their own degraded default to the latter.
type Classifier interface {
	Classify(ctx context.Context, text string) (TurnIntent, error)
}

// ClassifierFunc adapts a function to Classifier.
type ClassifierFunc func(ctx context.Context, text string) (TurnIntent, error)

func (f ClassifierFunc) Classify(ctx context.Context, text string) (TurnIntent, error) {
	return f(ctx, text)
}

const (
	// DefaultTimeout bounds one classification. It matches the semantic
	// capability router's budget: long enough for a small model, short enough
	// that a stalled provider cannot hold a user turn.
	DefaultTimeout = 3 * time.Second
	// DefaultCacheTTL bounds how long an answer is reused. Turn text repeats
	// often enough (retries, continuations, steering) that a short TTL removes
	// most duplicate calls.
	DefaultCacheTTL = 5 * time.Minute
)

// Bounded wraps a Classifier with the failure discipline every model call on a
// user-facing path needs: a hard timeout, a short-lived cache, and degradation
// to KindUnknown instead of an error the caller might mistake for a reading.
//
// The shape deliberately mirrors capability.SemanticRouter, which already runs
// this pattern in production for capability routing. A nil Inner degrades
// cleanly so offline builds, CI, and credential-less environments behave
// deterministically rather than failing.
type Bounded struct {
	Inner   Classifier
	Timeout time.Duration
	TTL     time.Duration
	Audit   *Audit

	// Now is injectable so cache-expiry behavior is testable without sleeping.
	Now func() time.Time

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	intent    TurnIntent
	expiresAt time.Time
}

func (b *Bounded) now() time.Time {
	if b != nil && b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *Bounded) timeout() time.Duration {
	if b != nil && b.Timeout > 0 {
		return b.Timeout
	}
	return DefaultTimeout
}

func (b *Bounded) ttl() time.Duration {
	if b != nil && b.TTL > 0 {
		return b.TTL
	}
	return DefaultCacheTTL
}

// Classify applies the cache, then the timeout, then records the outcome. It
// never returns an error: every failure mode is reported as KindUnknown so a
// caller cannot accidentally treat a provider outage as a reading of the turn.
func (b *Bounded) Classify(ctx context.Context, text string) (TurnIntent, error) {
	if b == nil || b.Inner == nil {
		return TurnIntent{}, nil
	}
	if text == "" {
		return TurnIntent{}, nil
	}

	if hit, ok := b.cacheGet(text); ok {
		b.Audit.recordCacheHit()
		return hit, nil
	}

	ctx, cancel := context.WithTimeout(ctx, b.timeout())
	defer cancel()

	started := b.now()
	got, err := b.Inner.Classify(ctx, text)
	elapsed := b.now().Sub(started)

	if err != nil || got.Unknown() {
		b.Audit.recordMiss(elapsed)
		return TurnIntent{}, nil
	}
	got.Source = SourceClassifier
	b.cachePut(text, got)
	b.Audit.recordHit(elapsed)
	return got, nil
}

func (b *Bounded) cacheGet(text string) (TurnIntent, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.cache[text]
	if !ok || !entry.expiresAt.After(b.now()) {
		return TurnIntent{}, false
	}
	return entry.intent, true
}

func (b *Bounded) cachePut(text string, t TurnIntent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cache == nil {
		b.cache = map[string]cacheEntry{}
	}
	b.cache[text] = cacheEntry{intent: t, expiresAt: b.now().Add(b.ttl())}
}

// Audit accumulates non-persisted classifier counters. It mirrors
// capability.Audit so routing and intent metrics report the same way.
type Audit struct {
	mu sync.Mutex

	Calls     int
	CacheHits int
	Answered  int
	Degraded  int
	LatencyMs int64
}

func (a *Audit) recordCacheHit() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.CacheHits++
}

func (a *Audit) recordHit(elapsed time.Duration) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Calls++
	a.Answered++
	a.LatencyMs += elapsed.Milliseconds()
}

func (a *Audit) recordMiss(elapsed time.Duration) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Calls++
	a.Degraded++
	a.LatencyMs += elapsed.Milliseconds()
}

// Snapshot returns a copy of the counters for reporting.
func (a *Audit) Snapshot() map[string]int64 {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return map[string]int64{
		"calls":      int64(a.Calls),
		"cache_hits": int64(a.CacheHits),
		"answered":   int64(a.Answered),
		"degraded":   int64(a.Degraded),
		"latency_ms": a.LatencyMs,
	}
}
