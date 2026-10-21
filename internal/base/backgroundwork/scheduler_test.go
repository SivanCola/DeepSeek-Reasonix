package backgroundwork

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestForegroundPreemptsAndQuietPeriodResumesBackground(t *testing.T) {
	now := time.Unix(100, 0)
	s := New(Options{Now: func() time.Time { return now }, After: func(time.Duration) <-chan time.Time { return make(chan time.Time) }})
	ctx, release, err := s.Acquire(context.Background(), Rebuild)
	if err != nil {
		t.Fatal(err)
	}
	end := s.Foreground()
	if !errors.Is(context.Cause(ctx), ErrPreempted) {
		t.Fatalf("cause=%v", context.Cause(ctx))
	}
	release()
	end()
	acquired := make(chan func(), 1)
	go func() {
		_, done, err := s.Acquire(context.Background(), Visible)
		if err == nil {
			acquired <- done
		}
	}()
	s.mu.Lock()
	now = now.Add(500 * time.Millisecond)
	s.signalLocked()
	s.mu.Unlock()
	select {
	case done := <-acquired:
		done()
	case <-time.After(time.Second):
		t.Fatal("background did not resume")
	}
}

func TestOnlyOneBackgroundReaderAndCancellationRemovesWaiter(t *testing.T) {
	s := New(Options{})
	_, release, err := s.Acquire(context.Background(), Rebuild)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.Acquire(ctx, Visible); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	s.mu.Lock()
	pending := len(s.waiters)
	s.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending=%d", pending)
	}
}

func TestProgressRefreshDoesNotCancelBackgroundOrExtendQuietPeriod(t *testing.T) {
	s := New(Options{})
	ctx, release, err := s.Acquire(context.Background(), Rebuild)
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		end := s.Read()
		end()
	}
	if ctx.Err() != nil {
		t.Fatal("progress refresh canceled its own producer")
	}
	release()
	s.mu.Lock()
	quiet := s.quietUntil
	s.mu.Unlock()
	if !quiet.IsZero() {
		t.Fatal("progress refresh imposed a quiet period")
	}
}

func waitQueued(t *testing.T, s *Scheduler, n int) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		s.mu.Lock()
		count, changed := len(s.waiters), s.changed
		s.mu.Unlock()
		if count == n {
			return
		}
		select {
		case <-changed:
		case <-deadline.C:
			t.Fatalf("queued=%d, want %d", count, n)
		}
	}
}

func TestVisibleWorkPrecedesQueuedRebuild(t *testing.T) {
	s := New(Options{})
	_, release, err := s.Acquire(context.Background(), Rebuild)
	if err != nil {
		t.Fatal(err)
	}
	type granted struct {
		priority Priority
		release  func()
	}
	results := make(chan granted, 2)
	for _, priority := range []Priority{Rebuild, Visible} {
		go func() {
			_, done, err := s.Acquire(t.Context(), priority)
			if err == nil {
				results <- granted{priority, done}
			}
		}()
	}
	waitQueued(t, s, 2)
	release()
	first := <-results
	if first.priority != Visible {
		t.Fatalf("first=%v", first.priority)
	}
	first.release()
	second := <-results
	second.release()
}

func TestAgedWorkGetsOneOpportunityBetweenForegroundOperations(t *testing.T) {
	now := time.Unix(100, 0)
	s := New(Options{Now: func() time.Time { return now }, After: func(time.Duration) <-chan time.Time { return make(chan time.Time) }})
	end := s.Foreground()
	result := make(chan func(), 1)
	go func() {
		_, done, err := s.Acquire(t.Context(), Rebuild)
		if err == nil {
			result <- done
		}
	}()
	waitQueued(t, s, 1)
	s.mu.Lock()
	now = now.Add(6 * time.Second)
	s.signalLocked()
	s.mu.Unlock()
	select {
	case done := <-result:
		done()
		t.Fatal("background overlapped active foreground")
	default:
	}
	end()
	select {
	case done := <-result:
		done()
	case <-time.After(time.Second):
		t.Fatal("aged work starved behind repeated quiet windows")
	}
}
