// Package backgroundwork gives interactive reads priority over disposable projections.
package backgroundwork

import (
	"context"
	"errors"
	"sync"
	"time"
)

type Priority int

const (
	Visible Priority = iota
	Incremental
	Rebuild
)

var ErrPreempted = errors.New("background work preempted by foreground")

type Options struct {
	Now   func() time.Time
	After func(time.Duration) <-chan time.Time
}

type waiter struct {
	priority Priority
	since    time.Time
}

type Scheduler struct {
	mu         sync.Mutex
	opts       Options
	waiters    []*waiter
	active     *context.CancelCauseFunc
	foreground int
	quietUntil time.Time
	changed    chan struct{}
}

var Shared = New(Options{})

func New(opts Options) *Scheduler {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.After == nil {
		opts.After = time.After
	}
	return &Scheduler{opts: opts, changed: make(chan struct{})}
}

func (s *Scheduler) signalLocked() { close(s.changed); s.changed = make(chan struct{}) }

func (s *Scheduler) Foreground() func() {
	return s.activity(true, 500*time.Millisecond)
}

// Read prevents new background work without repeatedly preempting it on progress refreshes.
func (s *Scheduler) Read() func() { return s.activity(false, 0) }

func (s *Scheduler) activity(preempt bool, quiet time.Duration) func() {
	s.mu.Lock()
	s.foreground++
	if preempt && s.active != nil {
		(*s.active)(ErrPreempted)
	}
	s.signalLocked()
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.foreground--
			if until := s.opts.Now().Add(quiet); quiet > 0 && until.After(s.quietUntil) {
				s.quietUntil = until
			}
			s.signalLocked()
			s.mu.Unlock()
		})
	}
}

func (s *Scheduler) first(now time.Time) *waiter {
	var best *waiter
	for _, w := range s.waiters {
		if now.Sub(w.since) >= 5*time.Second {
			return w
		}
		if best == nil || w.priority < best.priority {
			best = w
		}
	}
	return best
}

func (s *Scheduler) remove(w *waiter) {
	for i, pending := range s.waiters {
		if pending == w {
			s.waiters = append(s.waiters[:i], s.waiters[i+1:]...)
			break
		}
	}
	s.signalLocked()
}

func (s *Scheduler) Acquire(ctx context.Context, priority Priority) (context.Context, func(), error) {
	s.mu.Lock()
	w := &waiter{priority: priority, since: s.opts.Now()}
	s.waiters = append(s.waiters, w)
	s.signalLocked()
	for {
		if err := ctx.Err(); err != nil {
			s.remove(w)
			s.mu.Unlock()
			return nil, nil, err
		}
		now := s.opts.Now()
		delay := s.quietUntil.Sub(now)
		aged := now.Sub(w.since) >= 5*time.Second
		if s.foreground == 0 && s.active == nil && (delay <= 0 || aged) && s.first(now) == w {
			work, cancel := context.WithCancelCause(ctx)
			s.active = &cancel
			s.remove(w)
			s.mu.Unlock()
			var once sync.Once
			return work, func() {
				once.Do(func() {
					cancel(context.Canceled)
					s.mu.Lock()
					s.active = nil
					s.signalLocked()
					s.mu.Unlock()
				})
			}, nil
		}
		changed := s.changed
		var timer <-chan time.Time
		if s.foreground == 0 && s.active == nil && delay > 0 {
			timer = s.opts.After(delay)
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		case <-timer:
		}
		s.mu.Lock()
	}
}

func (s *Scheduler) Do(ctx context.Context, priority Priority, work func(context.Context) error) error {
	for {
		job, release, err := s.Acquire(ctx, priority)
		if err != nil {
			return err
		}
		err = work(job)
		preempted := errors.Is(context.Cause(job), ErrPreempted)
		release()
		if err == nil || !preempted {
			return err
		}
	}
}
