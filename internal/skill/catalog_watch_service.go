package skill

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"reasonix/internal/skill/skillwatch"
)

// This file hosts the Store side of the host-shared watch service. The service
// (internal/skill/skillwatch) owns physical watches; the store only derives a
// watch scope from the discovery rules, subscribes one logical subscription
// per discovery root, and invalidates its catalog when the coalesced
// notification arrives. First-party mutations keep calling Invalidate
// directly and never depend on filesystem events.

// watchScopeDirectories lists the directories discovery can visit under root
// for maxDepth levels: dot directories and discovery-skipped bodies
// (assets/node_modules/references/scripts) are excluded so content churn
// there cannot trigger catalog rebuilds, while nested skill entries stay
// covered. For a missing root it subscribes only the nearest existing
// ancestor — probing one missing path segment — so creating the root later
// still invalidates the snapshot. Symlink targets are traversed once.
func watchScopeDirectories(ctx context.Context, root string, maxDepth int) ([]string, bool) {
	root = filepath.Clean(root)
	probe := root
	for {
		if ctx.Err() != nil {
			return nil, false
		}
		info, err := os.Stat(probe)
		if err == nil && info.IsDir() {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return nil, true
		}
		probe = parent
	}
	if probe != root {
		// Missing root: watch only the next existing path segment upward.
		return []string{probe}, true
	}
	type pendingDir struct {
		path  string
		depth int
	}
	pending := []pendingDir{{path: root, depth: 0}}
	seen := map[string]bool{}
	var out []string
	for len(pending) > 0 {
		if ctx.Err() != nil {
			return nil, false
		}
		current := pending[0]
		pending = pending[1:]
		resolved := current.path
		if target, err := filepath.EvalSymlinks(current.path); err == nil {
			resolved = filepath.Clean(target)
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		info, err := os.Stat(current.path)
		if err != nil || !info.IsDir() {
			continue
		}
		out = append(out, current.path)
		if resolved != current.path {
			out = append(out, resolved)
		}
		if current.depth >= maxDepth {
			continue
		}
		entries, err := os.ReadDir(current.path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return nil, false
			}
			name := entry.Name()
			if shouldSkipScanDir(name) {
				continue
			}
			child := filepath.Join(current.path, name)
			if entry.IsDir() {
				pending = append(pending, pendingDir{path: child, depth: current.depth + 1})
				continue
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if target, err := os.Stat(child); err == nil && target.IsDir() {
					pending = append(pending, pendingDir{path: child, depth: current.depth + 1})
				}
			}
		}
	}
	sort.Strings(out)
	return out, true
}

// rootWatchHash summarizes one root's discovery-visible tree for the
// service's degraded-mode scan fallback. It hashes the same directory set the
// scope watches — entries contribute name, size, mtime and mode, matching the
// signature the retired Windows polling generation computed.
func rootWatchHash(ctx context.Context, root string, maxDepth int) ([sha256.Size]byte, int, bool) {
	if ctx.Err() != nil {
		return [sha256.Size]byte{}, 0, false
	}
	directories, ok := watchScopeDirectories(ctx, root, maxDepth)
	if !ok {
		return [sha256.Size]byte{}, 0, false
	}
	hash := sha256.New()
	entries := 0
	for _, dir := range directories {
		if ctx.Err() != nil {
			return [sha256.Size]byte{}, 0, false
		}
		list, err := os.ReadDir(dir)
		_, _ = fmt.Fprintf(hash, "%s\x00%v\x00", dir, err)
		for _, entry := range list {
			if ctx.Err() != nil {
				return [sha256.Size]byte{}, 0, false
			}
			info, statErr := entry.Info()
			if statErr != nil {
				_, _ = fmt.Fprintf(hash, "%s\x00%v\x00", entry.Name(), statErr)
				continue
			}
			entries++
			_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%d\x00%d\x00", entry.Name(), info.Size(), info.ModTime().UnixNano(), info.Mode())
		}
	}
	var sum [sha256.Size]byte
	copy(sum[:], hash.Sum(nil))
	return sum, entries, true
}

// subscribeHostWatch subscribes every discovery root to the shared service.
// Subscribe registers physical watches before returning, so the caller's first
// catalog scan cannot lose changes to a registration race.
func (s *Store) subscribeHostWatch() {
	s.watcherMu.Lock()
	if s.closed || s.hostWatchActive {
		s.watcherMu.Unlock()
		return
	}
	s.hostWatchActive = true
	s.watcherMu.Unlock()
	onChange := func(string) { s.Invalidate("filesystem changed") }
	for _, root := range s.roots() {
		sub := s.watchService.Subscribe(
			root.Dir, s.maxDepth, watchScopeDirectories, rootWatchHash, onChange,
		)
		s.watcherMu.Lock()
		s.watchSubs = append(s.watchSubs, sub)
		s.watcherMu.Unlock()
	}
}

// WatchDiagnostics exposes the shared service counters when this store watches
// through it. The boolean reports whether the service path is active.
func (s *Store) WatchDiagnostics() (skillwatch.Diagnostics, bool) {
	if s == nil || s.watchService == nil {
		return skillwatch.Diagnostics{}, false
	}
	return s.watchService.Diagnostics(), true
}
