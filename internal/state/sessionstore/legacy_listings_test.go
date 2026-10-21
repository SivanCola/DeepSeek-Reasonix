package sessionstore

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/state/sessionv4"
	"reasonix/internal/state/sessionv4/v4fixture"
)

func TestLegacyListingFlushesCompletedBatchBeforeFinishing(t *testing.T) {
	f := v4fixture.New(t)
	for i := range 34 {
		dir := f.Session(fmt.Sprintf("desktop-%024x", i+1), 3)
		f.Batch(dir, v4fixture.Ended, v4fixture.Event{Kind: "message/complete", Payload: v4fixture.Msg("a", "user", "hello")})
	}
	dir := filepath.Join(filepath.Dir(f.Root), "sessions")
	entered := make(chan struct{})
	calls := 0
	service := newLegacyListings(func(ctx context.Context, s sessionv4.Session) (sessionv4.Transcript, error) {
		calls++
		if calls == 33 {
			close(entered)
			<-ctx.Done()
			return sessionv4.Transcript{}, ctx.Err()
		}
		return s.TranscriptContext(ctx)
	})
	t.Cleanup(service.Close)
	service.Snapshot(dir)
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not reach blocked replay")
	}
	if n := len(loadV4Listings(dir).Entries); n < 32 {
		t.Fatalf("persisted=%d, want completed batch", n)
	}
	rows, status := service.Snapshot(dir)
	if len(rows) != 34 || status.CountsComplete {
		t.Fatalf("rows=%d status=%+v", len(rows), status)
	}
}

func TestLegacyListingCloseCancelsReplay(t *testing.T) {
	dir, _ := legacyFixture(t)
	entered := make(chan struct{})
	service := newLegacyListings(func(ctx context.Context, _ sessionv4.Session) (sessionv4.Transcript, error) {
		close(entered)
		<-ctx.Done()
		return sessionv4.Transcript{}, ctx.Err()
	})
	t.Cleanup(service.Close)
	service.Snapshot(dir)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	closed := make(chan struct{})
	go func() { service.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("close waited for backlog")
	}
}

func TestLegacyListingSnapshotDoesNotWaitForReplay(t *testing.T) {
	f := v4fixture.New(t)
	dir := f.Session("desktop-0123456789abcdef01234567", 3)
	f.Batch(dir, v4fixture.Ended, v4fixture.Event{Kind: "message/complete", Payload: v4fixture.Msg("a", "user", "hello")})
	sessions := filepath.Join(filepath.Dir(f.Root), "sessions")
	entered, release := make(chan struct{}), make(chan struct{})
	service := newLegacyListings(func(ctx context.Context, s sessionv4.Session) (sessionv4.Transcript, error) {
		close(entered)
		select {
		case <-release:
			return s.TranscriptContext(ctx)
		case <-ctx.Done():
			return sessionv4.Transcript{}, ctx.Err()
		}
	})
	t.Cleanup(service.Close)
	service.Snapshot(sessions)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("replay not started")
	}
	rows, status := service.Snapshot(sessions)
	if len(rows) != 1 || rows[0].CountsKnown || status.PendingMetadata != 1 || status.CountsComplete {
		t.Fatalf("rows=%+v status=%+v", rows, status)
	}
	close(release)
	awaitLegacyReady(t, service, sessions)
	rows, _ = service.Snapshot(sessions)
	if len(rows) != 1 || !rows[0].CountsKnown || rows[0].Preview != "hello" {
		t.Fatalf("rows=%+v", rows)
	}
}

func awaitLegacyReady(t *testing.T, service *LegacyListings, dir string) {
	t.Helper()
	changed := make(chan struct{}, 1)
	stop := service.Subscribe(func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	defer stop()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		_, status := service.Snapshot(dir)
		if status.CountsComplete {
			return
		}
		select {
		case <-changed:
		case <-timeout.C:
			t.Fatalf("listing did not settle: %+v", status)
		}
	}
}
