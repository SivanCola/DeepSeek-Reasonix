package resourcebudget

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"reasonix/internal/filelock"
)

const pollInterval = 100 * time.Millisecond

type entry struct {
	Version int       `json:"version"`
	Ticket  uint64    `json:"ticket"`
	Request Resources `json:"request"`
	Running bool      `json:"running"`
	token   string
}

type WaitInfo struct {
	Position int       `json:"position"`
	Running  int       `json:"running"`
	Request  Resources `json:"request"`
	Used     Resources `json:"reserved"`
	Budget   Resources `json:"budget"`
	Reason   string    `json:"reason"`
}

func (w WaitInfo) String() string {
	return fmt.Sprintf("Waiting for shared resource budget: queue #%d; %s; requested %d CPU / %d MiB; reserved %d/%d CPU, %d/%d MiB (%d running).", w.Position, w.Reason, w.Request.CPU, w.Request.MemoryMiB, w.Used.CPU, w.Budget.CPU, w.Used.MemoryMiB, w.Budget.MemoryMiB, w.Running)
}

type detailsKey struct{}

// WithWaitDetails emits initial and changed wait reasons outside scheduler
// locks. The callback must return promptly, just like tool progress callbacks.
func WithWaitDetails(ctx context.Context, notice func(WaitInfo)) context.Context {
	return context.WithValue(ctx, detailsKey{}, notice)
}

func entryPath(dir, token string) string { return filepath.Join(dir, "queue-v2", token+".json") }

func decode(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

// entriesLocked reclaims only dead OS-lock owners. No PID, clock, heartbeat,
// or age heuristic may delete a live task. Registration/admission hold
// queue-v2.lock; completed owners may concurrently remove their own entries.
func entriesLocked(dir string) ([]entry, error) {
	files, err := os.ReadDir(filepath.Join(dir, "queue-v2"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result []entry
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		token := strings.TrimSuffix(f.Name(), ".json")
		if len(token) != 32 {
			return nil, fmt.Errorf("invalid resource queue entry %s", f.Name())
		}
		if _, err := hex.DecodeString(token); err != nil {
			return nil, err
		}
		held, err := filelock.ExclusiveHeld(filepath.Join(dir, token+".lease"))
		if err != nil {
			return nil, err
		}
		if !held {
			if err := os.Remove(entryPath(dir, token)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			_ = os.Remove(filepath.Join(dir, token+".lease"))
			continue
		}
		data, err := os.ReadFile(entryPath(dir, token))
		if errors.Is(err, os.ErrNotExist) {
			continue // Owner released while this snapshot was read.
		}
		if err != nil {
			return nil, err
		}
		var e entry
		if err := decode(data, &e); err != nil {
			return nil, fmt.Errorf("resource queue entry %s: %w", f.Name(), err)
		}
		if e.Version != 2 || e.Ticket == 0 || e.Request.CPU < 1 || e.Request.MemoryMiB < 1 {
			return nil, fmt.Errorf("unsupported or invalid resource queue entry %s", f.Name())
		}
		e.token = token
		result = append(result, e)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Ticket < result[j].Ticket })
	// A crash between locking a nonce and publishing its entry leaves no queue
	// metadata. Reclaim those lock files too, including v1 leftovers. New random
	// nonces are never reused, and live locks (even not yet published) stay put.
	if files, err := os.ReadDir(dir); err == nil {
		for _, f := range files {
			if len(f.Name()) != 38 || !strings.HasSuffix(f.Name(), ".lease") {
				continue
			}
			if _, err := hex.DecodeString(strings.TrimSuffix(f.Name(), ".lease")); err != nil {
				continue
			}
			path := filepath.Join(dir, f.Name())
			if held, err := filelock.ExclusiveHeld(path); err == nil && !held {
				_ = os.Remove(path)
			}
		}
	}
	return result, nil
}

func inheritedLease(dir, token string) (*Lease, error) {
	cores := workerCores() // A v1 parent has no weighted reservation metadata.
	data, err := os.ReadFile(entryPath(dir, token))
	if err == nil {
		var e entry
		if err := decode(data, &e); err != nil {
			return nil, err
		}
		if e.Version != 2 || !e.Running || e.Request.CPU < 1 {
			return nil, errors.New("inherited resource lease has not been admitted")
		}
		cores = e.Request.CPU
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return &Lease{cores: cores, token: token, dir: dir}, nil
}

func taskRequest(ctx context.Context, env []string, c Config) (Resources, error) {
	r := c.Task
	for _, field := range []struct {
		name   string
		target *int
	}{
		{"REASONIX_TASK_CPU", &r.CPU}, {"REASONIX_TASK_MEMORY_MIB", &r.MemoryMiB},
	} {
		if raw := value(env, field.name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 {
				return r, fmt.Errorf("%s must be a positive integer", field.name)
			}
			*field.target = n
		}
	}
	if custom, ok := ctx.Value(requestKey{}).(Resources); ok {
		if custom.CPU != 0 {
			r.CPU = custom.CPU
		}
		if custom.MemoryMiB != 0 {
			r.MemoryMiB = custom.MemoryMiB
		}
	}
	return r, checkRequest(r, c.Budget)
}

// admission owns registration and the running transition under one short lock.
type admission struct {
	dir, token    string
	own           *entry
	releaseBridge func()
}

func (a *admission) step(ctx context.Context, env []string) (WaitInfo, bool, error) {
	unlock, err := filelock.Acquire(ctx, filepath.Join(a.dir, "queue-v2.lock"))
	if err != nil {
		return WaitInfo{}, false, err
	}
	defer unlock()
	c, err := loadConfig(a.dir)
	if err != nil {
		return WaitInfo{}, false, err
	}
	entries, err := entriesLocked(a.dir)
	if err != nil {
		return WaitInfo{}, false, err
	}
	if a.own == nil {
		r, err := taskRequest(ctx, env, c)
		if err != nil {
			return WaitInfo{}, false, err
		}
		ticket := uint64(1)
		if len(entries) > 0 {
			ticket = entries[len(entries)-1].Ticket + 1
		}
		if ticket == 0 {
			return WaitInfo{}, false, errors.New("resource queue ticket overflow")
		}
		a.own = &entry{Version: 2, Ticket: ticket, Request: r, token: a.token}
		if err := writeJSON(entryPath(a.dir, a.token), a.own); err != nil {
			return WaitInfo{}, false, err
		}
		entries = append(entries, *a.own)
	}
	if err := checkRequest(a.own.Request, c.Budget); err != nil {
		return WaitInfo{}, false, err
	}
	w := queueWaitInfo(entries, *a.own, c.Budget)
	if w.Reason != "" {
		return w, false, nil
	}
	// New owners share the v1 lane; an old exclusive owner blocks them all.
	// Neither permanent lock may be unlinked while participants may run.
	a.releaseBridge, err = filelock.TryAcquireMode(filepath.Join(a.dir, "heavy.lock"), filelock.ModeShared)
	if errors.Is(err, filelock.ErrHeld) {
		w.Reason = "legacy task owns the shared budget"
		return w, false, nil
	}
	if err != nil {
		return w, false, err
	}
	if err := ctx.Err(); err != nil {
		return w, false, err
	}
	a.own.Running = true
	if err := writeJSON(entryPath(a.dir, a.token), a.own); err != nil {
		return w, false, err
	}
	return w, true, nil
}

func queueWaitInfo(entries []entry, own entry, budget Resources) WaitInfo {
	w := WaitInfo{Request: own.Request, Budget: budget}
	for _, e := range entries {
		if e.Running {
			w.Running++
			w.Used.CPU += e.Request.CPU
			w.Used.MemoryMiB += e.Request.MemoryMiB
		} else if e.Ticket <= own.Ticket {
			w.Position++
		}
	}
	switch {
	case w.Position > 1:
		w.Reason = "earlier queued task (FIFO)"
	case own.Request.CPU > budget.CPU-w.Used.CPU && own.Request.MemoryMiB > budget.MemoryMiB-w.Used.MemoryMiB:
		w.Reason = "CPU and memory budget in use"
	case own.Request.CPU > budget.CPU-w.Used.CPU:
		w.Reason = "CPU budget in use"
	case own.Request.MemoryMiB > budget.MemoryMiB-w.Used.MemoryMiB:
		w.Reason = "memory budget in use"
	}
	return w
}

func acquireQueued(ctx context.Context, dir string, env []string) (_ *Lease, retErr error) {
	if err := os.MkdirAll(filepath.Join(dir, "queue-v2"), 0700); err != nil {
		return nil, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(nonce[:])
	tokenPath := filepath.Join(dir, token+".lease")
	releaseToken, err := filelock.TryAcquire(tokenPath)
	if err != nil {
		return nil, err
	}
	a := admission{dir: dir, token: token}
	cleanup := func() {
		// Metadata may disappear before its lock closes. A scanner can briefly
		// overcount a completed task, never undercount a running one.
		_ = os.Remove(entryPath(dir, token))
		if a.releaseBridge != nil {
			a.releaseBridge()
		}
		releaseToken()
		_ = os.Remove(tokenPath)
	}
	defer func() {
		if retErr != nil {
			cleanup()
		}
	}()
	var last WaitInfo
	notified := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, admitted, err := a.step(ctx, env)
		if err != nil {
			return nil, fmt.Errorf("resource admission: %w", err)
		}
		if admitted {
			return &Lease{cores: a.own.Request.CPU, token: token, dir: dir, release: cleanup}, nil
		}
		// step has released the scheduler lock before progress can re-enter it.
		if !notified {
			if notice, _ := ctx.Value(waitKey{}).(func()); notice != nil {
				notice()
			}
		}
		if !notified || info != last {
			if notice, _ := ctx.Value(detailsKey{}).(func(WaitInfo)); notice != nil {
				notice(info)
			}
		}
		last, notified = info, true
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

type Snapshot struct {
	Config     Config    `json:"config"`
	ConfigPath string    `json:"config_path"`
	Reserved   Resources `json:"reserved"`
	Running    int       `json:"running"`
	Waiting    int       `json:"waiting"`
}

// Status exposes reservations rather than pretending these are CPU/RSS samples.
func Status(ctx context.Context) (Snapshot, error) {
	var s Snapshot
	dir, err := directory(ctx)
	if err != nil {
		return s, err
	}
	unlock, err := filelock.Acquire(ctx, filepath.Join(dir, "queue-v2.lock"))
	if err != nil {
		return s, err
	}
	defer unlock()
	s.ConfigPath = configPath(dir)
	s.Config, err = loadConfig(dir)
	if err != nil {
		return s, err
	}
	entries, err := entriesLocked(dir)
	if err != nil {
		return s, err
	}
	for _, e := range entries {
		if e.Running {
			s.Running++
			s.Reserved.CPU += e.Request.CPU
			s.Reserved.MemoryMiB += e.Request.MemoryMiB
		} else {
			s.Waiting++
		}
	}
	return s, nil
}
