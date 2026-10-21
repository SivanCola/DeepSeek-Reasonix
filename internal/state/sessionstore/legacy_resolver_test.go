package sessionstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/base/filelock"
	"reasonix/internal/state/sessionv4"
	"reasonix/internal/state/sessionv4/v4fixture"
)

func legacyFixture(t *testing.T) (string, sessionv4.Session) {
	t.Helper()
	f := v4fixture.New(t)
	dir := f.Session("desktop-0123456789abcdef01234567", 3)
	f.Batch(dir, v4fixture.Ended, v4fixture.Event{Kind: "message/complete", Payload: v4fixture.Msg("a", "user", "hello")})
	s, err := sessionv4.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(filepath.Dir(f.Root), "sessions"), s
}

func TestLegacyResolverSharesConcurrentReplayAndRetainsFailedWrites(t *testing.T) {
	dir, source := legacyFixture(t)
	r := resolverFor(dir)
	var calls atomic.Int32
	replay := func(ctx context.Context, s sessionv4.Session) (sessionv4.Transcript, error) {
		calls.Add(1)
		return s.TranscriptContext(ctx)
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if _, err := r.resolve(context.Background(), dir, source, replay); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("replays=%d", calls.Load())
	}
	if err := os.MkdirAll(filepath.Join(dir, v4ListingName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := r.flush(context.Background(), dir, true); err == nil {
		t.Fatal("write should fail when destination is a directory")
	}
	if _, err := r.resolve(context.Background(), dir, source, replay); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("failed persistence discarded the memory result")
	}
	if err := os.Remove(filepath.Join(dir, v4ListingName)); err != nil {
		t.Fatal(err)
	}
	if err := r.flush(context.Background(), dir, true); err != nil {
		t.Fatal(err)
	}
	restarted := &legacyResolver{gate: make(chan struct{}, 1)}
	if _, err := restarted.resolve(context.Background(), dir, source, replay); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("restart replayed a persisted result")
	}
}

func TestLegacyCacheLockIsNonblocking(t *testing.T) {
	dir, source := legacyFixture(t)
	r := resolverFor(dir)
	if _, err := r.resolve(context.Background(), dir, source, readLegacyTranscript); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	release, err := filelock.TryAcquire(filepath.Join(dir, v4ListingName+".lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := r.flush(context.Background(), dir, true); !errors.Is(err, filelock.ErrHeld) {
		t.Fatalf("error=%v", err)
	}
}

func TestLegacyCacheRejectsChangedSourceDuringReplay(t *testing.T) {
	dir, source := legacyFixture(t)
	r := resolverFor(dir)
	_, err := r.resolve(context.Background(), dir, source, func(ctx context.Context, s sessionv4.Session) (sessionv4.Transcript, error) {
		result, err := s.TranscriptContext(ctx)
		f, openErr := os.OpenFile(filepath.Join(s.Dir, "events.frames"), os.O_APPEND|os.O_WRONLY, 0)
		if openErr != nil {
			t.Fatal(openErr)
		}
		if _, e := f.Write([]byte("partial next frame")); e != nil {
			t.Fatal(e)
		}
		if e := f.Close(); e != nil {
			t.Fatal(e)
		}
		return result, err
	})
	if !errors.Is(err, errLegacySourceChanged) {
		t.Fatalf("error=%v", err)
	}
	if _, ok := r.cache.get(source); ok {
		t.Fatal("published a stale result")
	}
}

func TestLegacyCacheIdentitySurvivesEquivalentTimestampEncoding(t *testing.T) {
	dir, source := legacyFixture(t)
	path := filepath.Join(source.Dir, "manifest.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte(source.Manifest.CreatedAt.Format(time.RFC3339Nano)), []byte(source.Manifest.CreatedAt.Format("2006-01-02T15:04:05.999999999-07:00")))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	source, err = sessionv4.Open(source.Dir)
	if err != nil {
		t.Fatal(err)
	}
	r := resolverFor(dir)
	if _, err := r.resolve(context.Background(), dir, source, readLegacyTranscript); err != nil {
		t.Fatal(err)
	}
	if err := r.flush(context.Background(), dir, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadV4Listings(dir).get(source); !ok {
		t.Fatal("equivalent timestamp encoding invalidated the persisted summary")
	}
}

func TestLegacyCanceledPersistenceRetainsCompletedSummary(t *testing.T) {
	dir, source := legacyFixture(t)
	r := resolverFor(dir)
	if _, err := r.resolve(context.Background(), dir, source, readLegacyTranscript); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.flush(ctx, dir, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if !r.cache.dirty {
		t.Fatal("canceled persistence discarded the pending cache")
	}
	if err := r.flush(context.Background(), dir, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadV4Listings(dir).get(source); !ok {
		t.Fatal("completed summary was lost")
	}
}

func TestLegacyOlderCacheIsRebuiltWithoutChangingSource(t *testing.T) {
	dir, source := legacyFixture(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := &v4ListingCache{Format: 1, Entries: map[string]v4ListingEntry{source.Manifest.SessionID: {LogSize: source.LogSize, LogMod: source.LogModTime.UnixNano(), Listing: sessionv4.Listing{Preview: "obsolete", Turns: 99}}}}
	raw, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, v4ListingName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(source.Dir, "events.frames"))
	if err != nil {
		t.Fatal(err)
	}
	r := resolverFor(dir)
	listing, err := r.resolve(context.Background(), dir, source, readLegacyTranscript)
	if err != nil || listing.Preview != "hello" || listing.Turns != 1 {
		t.Fatalf("listing=%+v error=%v", listing, err)
	}
	if err := r.flush(context.Background(), dir, true); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(source.Dir, "events.frames"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("cache rebuild changed authoritative history")
	}
}
