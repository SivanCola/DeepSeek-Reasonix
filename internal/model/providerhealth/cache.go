package providerhealth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"sync"
	"time"

	"reasonix/internal/base/netclient"
	"reasonix/internal/contract/config"
)

const freshFor = time.Minute

type cached struct {
	done    chan struct{}
	reading Reading
	at      time.Time
}

// Checker coalesces reads by effective credentials and configuration, for one runtime.
type Checker struct {
	mu      sync.Mutex
	entries map[[32]byte]*cached
}

func (c *Checker) Read(ctx context.Context, p *config.ProviderEntry, spec netclient.ProxySpec) Reading {
	unknown := Reading{Connection: "unknown", Wallet: "unknown"}
	if ctx.Err() != nil {
		return unknown
	}
	// Only the digest is retained: rotating a key in the same slot invalidates it.
	data, err := json.Marshal(struct {
		Entry *config.ProviderEntry
		Key   string
		Proxy netclient.ProxySpec
	}{p, p.APIKey(), spec})
	if err != nil {
		return unknown
	}
	key := sha256.Sum256(data)
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[[32]byte]*cached)
	}
	if prior := c.entries[key]; prior != nil && (prior.done != nil || time.Since(prior.at) < freshFor) {
		wait := prior.done
		got := prior.reading
		c.mu.Unlock()
		if wait == nil {
			return got
		}
		select {
		case <-wait:
			c.mu.Lock()
			got = prior.reading
			c.mu.Unlock()
			return got
		case <-ctx.Done():
			return unknown
		}
	}
	for id, entry := range c.entries {
		if entry.done == nil && time.Since(entry.at) >= freshFor {
			delete(c.entries, id)
		}
	}
	if len(c.entries) >= 128 {
		c.mu.Unlock()
		return unknown
	}
	entry := &cached{done: make(chan struct{})}
	c.entries[key] = entry
	c.mu.Unlock()
	got := check(ctx, p, spec)
	c.mu.Lock()
	entry.reading, entry.at = got, time.Now()
	entry.reading.CheckedAt = entry.at.UnixMilli()
	if ctx.Err() != nil {
		delete(c.entries, key)
	}
	close(entry.done)
	entry.done = nil
	got = entry.reading
	c.mu.Unlock()
	return got
}
