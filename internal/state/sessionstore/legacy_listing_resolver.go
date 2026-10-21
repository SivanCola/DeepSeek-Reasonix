package sessionstore

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"reasonix/internal/state/sessionv4"
)

var errLegacySourceChanged = errors.New("legacy source changed during replay")

type legacyReplay func(context.Context, sessionv4.Session) (sessionv4.Transcript, error)

type legacyResolver struct {
	gate  chan struct{}
	cache *v4ListingCache
	write legacyWriteState
}

type legacyWriteState struct {
	next     time.Time
	failures int
	err      error
}

var legacyResolvers sync.Map

func resolverFor(dir string) *legacyResolver {
	r := &legacyResolver{gate: make(chan struct{}, 1)}
	actual, _ := legacyResolvers.LoadOrStore(filepath.Clean(dir), r)
	return actual.(*legacyResolver)
}

func (r *legacyResolver) acquire(ctx context.Context, dir string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case r.gate <- struct{}{}:
		if r.cache == nil {
			r.cache = loadV4Listings(dir)
		}
		return func() { <-r.gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *legacyResolver) lookup(s sessionv4.Session) (sessionv4.Listing, bool) {
	if l, ok := r.cache.get(s); ok {
		return l, true
	}
	if l, ok := s.CachedListing(); ok {
		r.cache.put(s, l)
		return l, true
	}
	return sessionv4.Listing{}, false
}

func (r *legacyResolver) resolve(ctx context.Context, dir string, s sessionv4.Session, replay legacyReplay) (sessionv4.Listing, error) {
	release, err := r.acquire(ctx, dir)
	if err != nil {
		return sessionv4.Listing{}, err
	}
	defer release()
	if l, ok := r.lookup(s); ok {
		return l, nil
	}
	t, err := replay(ctx, s)
	if err != nil {
		return sessionv4.Listing{}, err
	}
	current, err := sessionv4.Open(s.Dir)
	if err != nil {
		return sessionv4.Listing{}, err
	}
	if !current.Manifest.Equal(s.Manifest) || current.LogSize != s.LogSize || !current.LogModTime.Equal(s.LogModTime) {
		return sessionv4.Listing{}, errLegacySourceChanged
	}
	preview, turns := SessionPreviewFromMessages(t.Messages)
	l := sessionv4.Listing{Title: t.Title, ModelRef: t.ModelRef, Preview: preview, Turns: turns}
	r.cache.put(s, l)
	return l, nil
}

func (r *legacyResolver) flush(ctx context.Context, dir string, force bool) error {
	release, err := r.acquire(ctx, dir)
	if err != nil {
		return err
	}
	defer release()
	if !r.cache.dirty {
		return nil
	}
	if !force && time.Now().Before(r.write.next) {
		return r.write.err
	}
	err = r.cache.save(ctx, dir)
	if err == nil {
		r.write = legacyWriteState{}
		return nil
	}
	r.write.failures++
	delays := []time.Duration{time.Second, 5 * time.Second, 30 * time.Second, time.Minute}
	delay := delays[min(r.write.failures-1, len(delays)-1)]
	r.write.next, r.write.err = time.Now().Add(delay), err
	slog.Warn("legacy listing cache could not be persisted", "error", err, "retry_after", delay)
	return err
}

func readLegacyTranscript(ctx context.Context, s sessionv4.Session) (sessionv4.Transcript, error) {
	return s.TranscriptContext(ctx)
}
