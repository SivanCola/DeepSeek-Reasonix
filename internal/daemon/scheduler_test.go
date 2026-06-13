package daemon

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
)

func TestNextDailyTime(t *testing.T) {
	// 09:00 when it's 08:00 → today 09:00
	base := time.Date(2026, 6, 13, 8, 0, 0, 0, time.Local)
	got := nextDailyTime("09:00", base)
	want := time.Date(2026, 6, 13, 9, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("nextDailyTime(09:00, 08:00) = %v, want %v", got, want)
	}

	// 09:00 when it's 10:00 → tomorrow 09:00
	base = time.Date(2026, 6, 13, 10, 0, 0, 0, time.Local)
	got = nextDailyTime("09:00", base)
	want = time.Date(2026, 6, 14, 9, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("nextDailyTime(09:00, 10:00) = %v, want %v", got, want)
	}

	// Invalid spec.
	got = nextDailyTime("25:00", base)
	if !got.IsZero() {
		t.Errorf("nextDailyTime(25:00) should be zero, got %v", got)
	}

	got = nextDailyTime("abc", base)
	if !got.IsZero() {
		t.Errorf("nextDailyTime(abc) should be zero, got %v", got)
	}
}

func TestSchedulerComputeNextWakeupInterval(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	d := New(Options{SessionDir: t.TempDir()})
	s := NewScheduler(d, logger)

	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	sched := agent.RuntimeSchedMeta{
		Interval:     1 * time.Hour,
		Enabled:      true,
		LastWakeupAt: now.Add(-30 * time.Minute), // last wakeup 30min ago
	}

	next := s.computeNextWakeup(sched, now)
	// Last wakeup was 30min ago, interval is 1h → next in 30min.
	want := sched.LastWakeupAt.Add(1 * time.Hour)
	if !next.Equal(want) {
		t.Errorf("computeNextWakeup(interval=1h, lastWakeup=30min ago) = %v, want %v", next, want)
	}

	// Last wakeup was 2h ago → should advance to next interval from now.
	sched.LastWakeupAt = now.Add(-2 * time.Hour)
	next = s.computeNextWakeup(sched, now)
	if next.Before(now) {
		t.Errorf("computeNextWakeup should not return past time, got %v (now=%v)", next, now)
	}
}

func TestSchedulerComputeNextWakeupDaily(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	d := New(Options{SessionDir: t.TempDir()})
	s := NewScheduler(d, logger)

	now := time.Date(2026, 6, 13, 8, 0, 0, 0, time.Local)
	sched := agent.RuntimeSchedMeta{
		DailyAt: "09:00",
		Enabled: true,
	}

	next := s.computeNextWakeup(sched, now)
	want := time.Date(2026, 6, 13, 9, 0, 0, 0, time.Local)
	if !next.Equal(want) {
		t.Errorf("computeNextWakeup(daily=09:00, now=08:00) = %v, want %v", next, want)
	}
}

func TestSchedulerShouldWakeupGuards(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	d := New(Options{SessionDir: t.TempDir()})
	s := NewScheduler(d, logger)

	now := time.Now()
	pastTime := now.Add(-1 * time.Minute)

	tests := []struct {
		name   string
		entry  *SessionEntry
		expect bool
	}{
		{
			name: "disabled scheduler",
			entry: &SessionEntry{
				ID: "t1",
				Runtime: agent.RuntimeMeta{
					Goal:      agent.RuntimeGoalMeta{Status: "running"},
					Run:       agent.RuntimeRunMeta{Status: "idle"},
					Scheduler: agent.RuntimeSchedMeta{Enabled: false, Interval: time.Hour, NextWakeupAt: pastTime},
				},
			},
			expect: false,
		},
		{
			name: "no active goal",
			entry: &SessionEntry{
				ID: "t2",
				Runtime: agent.RuntimeMeta{
					Goal:      agent.RuntimeGoalMeta{Status: "complete"},
					Run:       agent.RuntimeRunMeta{Status: "idle"},
					Scheduler: agent.RuntimeSchedMeta{Enabled: true, Interval: time.Hour, NextWakeupAt: pastTime},
				},
			},
			expect: false,
		},
		{
			name: "run already in-flight",
			entry: &SessionEntry{
				ID: "t3",
				Runtime: agent.RuntimeMeta{
					Goal:      agent.RuntimeGoalMeta{Status: "running"},
					Run:       agent.RuntimeRunMeta{Status: "running"},
					Scheduler: agent.RuntimeSchedMeta{Enabled: true, Interval: time.Hour, NextWakeupAt: pastTime},
				},
			},
			expect: false,
		},
		{
			name: "next wakeup in future",
			entry: &SessionEntry{
				ID: "t4",
				Runtime: agent.RuntimeMeta{
					Goal:      agent.RuntimeGoalMeta{Status: "running"},
					Run:       agent.RuntimeRunMeta{Status: "idle"},
					Scheduler: agent.RuntimeSchedMeta{Enabled: true, Interval: time.Hour, NextWakeupAt: now.Add(time.Hour)},
				},
			},
			expect: false,
		},
		{
			name: "all guards pass — should wake",
			entry: &SessionEntry{
				ID: "t5",
				Runtime: agent.RuntimeMeta{
					Goal:      agent.RuntimeGoalMeta{Status: "running"},
					Run:       agent.RuntimeRunMeta{Status: "idle"},
					Scheduler: agent.RuntimeSchedMeta{Enabled: true, Interval: time.Hour, NextWakeupAt: pastTime},
				},
			},
			expect: true,
		},
		{
			name: "blocked goal should also wake",
			entry: &SessionEntry{
				ID: "t6",
				Runtime: agent.RuntimeMeta{
					Goal:      agent.RuntimeGoalMeta{Status: "blocked"},
					Run:       agent.RuntimeRunMeta{Status: "idle"},
					Scheduler: agent.RuntimeSchedMeta{Enabled: true, Interval: time.Hour, NextWakeupAt: pastTime},
				},
			},
			expect: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.shouldWakeup(tt.entry, now)
			if got != tt.expect {
				t.Errorf("shouldWakeup() = %v, want %v", got, tt.expect)
			}
		})
	}
}

func TestSchedulerWakeupPersists(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "sched-test.jsonl")
	os.WriteFile(sess, []byte(`{}`), 0o644)

	meta := agent.RuntimeMeta{
		SessionID: "sched-test",
		Goal:      agent.RuntimeGoalMeta{Text: "do work", Status: "running"},
		Run:       agent.RuntimeRunMeta{Status: "idle"},
		Scheduler: agent.RuntimeSchedMeta{
			Enabled:  true,
			Interval: 1 * time.Hour,
		},
	}
	agent.SaveRuntimeMeta(sess, meta)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	d := New(Options{SessionDir: dir})
	d.scanSessions()
	s := NewScheduler(d, logger)

	now := time.Now()
	d.mu.RLock()
	entry := d.registry["sched-test"]
	d.mu.RUnlock()

	// Force the NextWakeupAt to the past so it fires.
	d.mu.Lock()
	entry.Runtime.Scheduler.NextWakeupAt = now.Add(-1 * time.Minute)
	d.mu.Unlock()

	s.wakeup(entry, now)

	// Verify runtime was updated.
	d.mu.RLock()
	rt := entry.Runtime
	d.mu.RUnlock()

	if rt.Run.Status != "pending_continue" {
		t.Errorf("Run.Status = %q, want pending_continue", rt.Run.Status)
	}
	if rt.Scheduler.LastWakeupReason != "cron" {
		t.Errorf("LastWakeupReason = %q, want cron", rt.Scheduler.LastWakeupReason)
	}
	if rt.Scheduler.NextWakeupAt.IsZero() {
		t.Error("NextWakeupAt should be set after wakeup")
	}
	if !rt.Scheduler.NextWakeupAt.After(now) {
		t.Errorf("NextWakeupAt should be in the future, got %v", rt.Scheduler.NextWakeupAt)
	}

	// Verify persisted to disk.
	loaded, ok, err := agent.LoadRuntimeMeta(sess)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta: err=%v ok=%v", err, ok)
	}
	if loaded.Run.Status != "pending_continue" {
		t.Errorf("persisted Run.Status = %q", loaded.Run.Status)
	}
}

func TestSchedulerWakeupRespectsDailyBudget(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "sched-budget.jsonl")
	if err := os.WriteFile(sess, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	now := time.Now().UTC()
	if err := agent.SaveRuntimeMeta(sess, agent.RuntimeMeta{
		SessionID: "sched-budget",
		Goal:      agent.RuntimeGoalMeta{Text: "do work", Status: "running"},
		Run:       agent.RuntimeRunMeta{Status: "idle"},
		Scheduler: agent.RuntimeSchedMeta{
			Enabled:      true,
			Interval:     time.Hour,
			NextWakeupAt: now.Add(-time.Minute),
		},
		Budget: agent.RuntimeBudgetMeta{
			DailyWakeupLimit: 1,
			DailyWakeups:     1,
			WindowStartedAt:  budgetWindowStart(now),
		},
	}); err != nil {
		t.Fatalf("SaveRuntimeMeta: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	d := New(Options{SessionDir: dir})
	d.scanSessions()
	s := NewScheduler(d, logger)

	d.mu.RLock()
	entry := d.registry["sched-budget"]
	d.mu.RUnlock()
	s.wakeup(entry, now)

	select {
	case intent := <-d.intentCh:
		t.Fatalf("budget-blocked scheduler should not enqueue intent: %+v", intent)
	default:
	}

	loaded, ok, err := agent.LoadRuntimeMeta(sess)
	if err != nil || !ok {
		t.Fatalf("LoadRuntimeMeta: err=%v ok=%v", err, ok)
	}
	if loaded.Run.Status != "idle" {
		t.Fatalf("Run.Status = %q, want idle", loaded.Run.Status)
	}
	if loaded.Budget.LastBlockedReason == "" {
		t.Fatalf("budget block reason missing: %+v", loaded.Budget)
	}
	if loaded.Scheduler.LastWakeupReason != "budget_blocked:cron" {
		t.Fatalf("LastWakeupReason = %q, want budget_blocked:cron", loaded.Scheduler.LastWakeupReason)
	}
	if !loaded.Scheduler.NextWakeupAt.After(now) {
		t.Fatalf("NextWakeupAt should advance after budget block, got %v", loaded.Scheduler.NextWakeupAt)
	}
}

func TestSchedulerWakeupRechecksRuntimeBeforePersist(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "recheck.jsonl")
	os.WriteFile(sess, []byte(`{}`), 0o644)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	d := New(Options{SessionDir: dir})
	s := NewScheduler(d, logger)
	now := time.Now()
	entry := &SessionEntry{
		ID:   "recheck",
		Path: sess,
		Runtime: agent.RuntimeMeta{
			SessionID: "recheck",
			Goal:      agent.RuntimeGoalMeta{Text: "work", Status: "running"},
			Run:       agent.RuntimeRunMeta{Status: "idle"},
			Scheduler: agent.RuntimeSchedMeta{
				Enabled:      true,
				Interval:     time.Hour,
				NextWakeupAt: now.Add(-time.Minute),
			},
		},
	}
	d.mu.Lock()
	d.registry["recheck"] = entry
	entry.Runtime.Goal.Status = "stopped"
	d.mu.Unlock()

	s.wakeup(entry, now)

	d.mu.RLock()
	rt := entry.Runtime
	d.mu.RUnlock()
	if rt.Run.Status == "pending_continue" {
		t.Fatal("wakeup should not fire after runtime no longer passes guards")
	}
	if rt.Scheduler.LastWakeupEventID != "" {
		t.Fatalf("wakeup event should remain empty, got %q", rt.Scheduler.LastWakeupEventID)
	}
}

func TestSchedulerDedupPreventsDoubleFire(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "dedup.jsonl")
	os.WriteFile(sess, []byte(`{}`), 0o644)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	d := New(Options{SessionDir: dir})
	s := NewScheduler(d, logger)

	now := time.Now()
	entry := &SessionEntry{
		ID:   "dedup",
		Path: sess,
		Runtime: agent.RuntimeMeta{
			SessionID: "dedup",
			Goal:      agent.RuntimeGoalMeta{Text: "work", Status: "running"},
			Run:       agent.RuntimeRunMeta{Status: "idle"},
			Scheduler: agent.RuntimeSchedMeta{
				Enabled:      true,
				Interval:     1 * time.Hour,
				NextWakeupAt: now.Add(-1 * time.Minute),
			},
		},
	}

	d.mu.Lock()
	d.registry["dedup"] = entry
	d.mu.Unlock()

	// First check should fire.
	if !s.shouldWakeup(entry, now) {
		t.Fatal("first shouldWakeup should return true")
	}

	// Fire the wakeup.
	s.wakeup(entry, now)

	// Second check with same time window should NOT fire (dedup).
	if s.shouldWakeup(entry, now) {
		t.Fatal("second shouldWakeup should return false (dedup)")
	}
}
