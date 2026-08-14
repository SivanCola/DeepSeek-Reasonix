package sandbox

import (
	"context"
	"sync"
)

// WritableRootSet is the session-scoped writable directory manager. Baseline
// is the workspace plus configured allow_write and --add-dir roots. Session
// holds directories approved for the rest of this logical session. Per-call
// roots ride on the execution context and never leak to other tool calls.
type WritableRootSet struct {
	mu       sync.RWMutex
	baseline []string
	session  []string
}

// NewWritableRootSet builds a set with the given baseline roots.
func NewWritableRootSet(baseline []string) *WritableRootSet {
	return &WritableRootSet{baseline: CollapseWriteRoots(canonicalDirs(baseline))}
}

// ReplaceBaseline swaps the configured roots (workspace, allow_write, --add-dir)
// without dropping session grants.
func (s *WritableRootSet) ReplaceBaseline(roots []string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.baseline = CollapseWriteRoots(canonicalDirs(roots))
	s.mu.Unlock()
}

// GrantSession adds directories to the session grant set.
func (s *WritableRootSet) GrantSession(dirs []string) {
	if s == nil || len(dirs) == 0 {
		return
	}
	s.mu.Lock()
	s.session = CollapseWriteRoots(append(append([]string{}, s.session...), canonicalDirs(dirs)...))
	s.mu.Unlock()
}

// ClearSession drops session grants. Project baseline is left intact.
func (s *WritableRootSet) ClearSession() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.session = nil
	s.mu.Unlock()
}

// SessionRoots returns a copy of the session-approved directories.
func (s *WritableRootSet) SessionRoots() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.session...)
}

// Snapshot returns baseline plus session grants, collapsed.
func (s *WritableRootSet) Snapshot() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return CollapseWriteRoots(append(append([]string{}, s.baseline...), s.session...))
}

// Effective returns baseline + session + per-call roots from ctx.
func (s *WritableRootSet) Effective(ctx context.Context) []string {
	return CollapseWriteRoots(append(s.Snapshot(), PerCallWriteRoots(ctx)...))
}

// Covers reports whether dir is inside the current baseline+session snapshot.
func (s *WritableRootSet) Covers(dir string) bool {
	dir = canonicalDir(dir)
	if dir == "" {
		return false
	}
	for _, root := range s.Snapshot() {
		if PathWithin(root, dir) {
			return true
		}
	}
	return false
}

// Missing returns the subset of dirs not already covered by the snapshot.
func (s *WritableRootSet) Missing(dirs []string) []string {
	if len(dirs) == 0 {
		return nil
	}
	snap := s.Snapshot()
	var missing []string
	for _, dir := range CollapseWriteRoots(canonicalDirs(dirs)) {
		covered := false
		for _, root := range snap {
			if PathWithin(root, dir) {
				covered = true
				break
			}
		}
		if !covered {
			missing = append(missing, dir)
		}
	}
	return missing
}

// CloneRestricted returns a new set whose baseline is the intersection of this
// set's snapshot with cap. The clone has no session grants. An empty cap
// copies the current snapshot (inherit, do not expand).
func (s *WritableRootSet) CloneRestricted(cap []string) *WritableRootSet {
	snap := s.Snapshot()
	if len(cap) == 0 {
		return NewWritableRootSet(snap)
	}
	return NewWritableRootSet(IntersectWriteRoots(snap, cap))
}

// IntersectWriteRoots returns directories that sit in both a and b, preferring
// the more specific path when one side is an ancestor of the other.
func IntersectWriteRoots(a, b []string) []string {
	a = CollapseWriteRoots(canonicalDirs(a))
	b = CollapseWriteRoots(canonicalDirs(b))
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	var out []string
	for _, left := range a {
		for _, right := range b {
			switch {
			case PathWithin(left, right):
				out = append(out, right)
			case PathWithin(right, left):
				out = append(out, left)
			}
		}
	}
	return CollapseWriteRoots(out)
}

type perCallWriteRootsKey struct{}

// WithPerCallWriteRoots stamps once-only writable directories onto ctx.
func WithPerCallWriteRoots(ctx context.Context, dirs []string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	dirs = CollapseWriteRoots(canonicalDirs(dirs))
	if len(dirs) == 0 {
		return ctx
	}
	return context.WithValue(ctx, perCallWriteRootsKey{}, dirs)
}

// PerCallWriteRoots returns once-only writable directories from ctx.
func PerCallWriteRoots(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	dirs, _ := ctx.Value(perCallWriteRootsKey{}).([]string)
	return append([]string(nil), dirs...)
}

func canonicalDirs(dirs []string) []string {
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if resolved := canonicalDir(dir); resolved != "" {
			out = append(out, resolved)
		}
	}
	return out
}
