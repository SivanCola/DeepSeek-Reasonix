package skill

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/fsnotify/fsnotify"
)

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.watcherMu.Lock()
	if s.closed {
		s.watcherMu.Unlock()
		return nil
	}
	s.closed = true
	s.watcherGeneration++
	watcher, done, cancel := s.watcher, s.watcherDone, s.watcherLifecycle.cancel
	s.watcher, s.watcherDone, s.watcherLifecycle.cancel = nil, nil, nil
	s.watcherLifecycle.active = false
	s.watcherMu.Unlock()
	s.catalogMu.Lock()
	if s.catalogFlight != nil && s.catalogFlight.cancel != nil {
		s.catalogFlight.cancel()
	}
	s.catalogMu.Unlock()
	if watcher != nil {
		_ = watcher.Close()
	}
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	return nil
}

func (s *Store) ensureWatcher() {
	if s == nil || s.disableDiscovery {
		return
	}
	s.watcherMu.Lock()
	if s.closed || s.watcherLifecycle.active {
		s.watcherMu.Unlock()
		return
	}
	if runtime.GOOS == "windows" {
		ctx, cancel := context.WithCancel(context.Background())
		s.watcherGeneration++
		generation := s.watcherGeneration
		done := make(chan struct{})
		s.watcherDone, s.watcherLifecycle.cancel, s.watcherLifecycle.active = done, cancel, true
		s.watcherMu.Unlock()
		go s.pollCatalog(ctx, generation, done)
		return
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		s.watcherMu.Unlock()
		return
	}
	s.watcherGeneration++
	generation := s.watcherGeneration
	done := make(chan struct{})
	s.watcher, s.watcherDone, s.watcherLifecycle.active = watcher, done, true
	s.watcherMu.Unlock()

	s.refreshWatcherPaths(watcher, generation)
	go s.watchCatalog(watcher, generation, done)
}

func (s *Store) watchCatalog(watcher *fsnotify.Watcher, generation uint64, done chan struct{}) {
	defer close(done)
	defer func() {
		s.watcherMu.Lock()
		if s.watcher == watcher && s.watcherGeneration == generation {
			s.watcher = nil
			s.watcherDone = nil
			s.watcherLifecycle.active = false
		}
		s.watcherMu.Unlock()
	}()
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok || !s.watcherCurrent(watcher, generation) {
				return
			}
			if event.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename|fsnotify.Write|fsnotify.Chmod) == 0 {
				continue
			}
			s.Invalidate("filesystem changed")
			// A create/rename can introduce a directory, symlink target, or a
			// previously missing root. Rebuild the subscriptions from the roots.
			s.refreshWatcherPaths(watcher, generation)
		case _, ok := <-watcher.Errors:
			if !ok || !s.watcherCurrent(watcher, generation) {
				return
			}
			s.Invalidate("filesystem watcher failed")
			_ = watcher.Close()
			return
		}
	}
}

// Windows fsnotify Add can block inside ReadDirectoryChangesW while another
// goroutine closes the watcher. A session teardown must never wait on that
// uninterruptible registration path, so Windows uses a cancellable, bounded
// polling generation instead. Host install/config operations still invalidate
// synchronously; polling covers external edits and missing-root creation.
func (s *Store) pollCatalog(ctx context.Context, generation uint64, done chan struct{}) {
	defer close(done)
	defer func() {
		s.watcherMu.Lock()
		if s.watcherGeneration == generation {
			s.watcherDone = nil
			s.watcherLifecycle.cancel = nil
			s.watcherLifecycle.active = false
		}
		s.watcherMu.Unlock()
	}()
	previous := s.catalogWatchSignature()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current := s.catalogWatchSignature()
			if current != previous {
				previous = current
				s.Invalidate("filesystem changed")
			}
		}
	}
}

func (s *Store) catalogWatchSignature() [sha256.Size]byte {
	hash := sha256.New()
	for _, root := range s.roots() {
		for _, dir := range watchDirectories(root.Dir, s.maxDepth) {
			entries, err := os.ReadDir(dir)
			_, _ = fmt.Fprintf(hash, "%s\x00%v\x00", dir, err)
			for _, entry := range entries {
				info, statErr := entry.Info()
				if statErr != nil {
					_, _ = fmt.Fprintf(hash, "%s\x00%v\x00", entry.Name(), statErr)
					continue
				}
				_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%d\x00%d\x00", entry.Name(), info.Size(), info.ModTime().UnixNano(), info.Mode())
			}
		}
	}
	var signature [sha256.Size]byte
	copy(signature[:], hash.Sum(nil))
	return signature
}

func (s *Store) watcherCurrent(watcher *fsnotify.Watcher, generation uint64) bool {
	s.watcherMu.Lock()
	defer s.watcherMu.Unlock()
	return !s.closed && s.watcher == watcher && s.watcherGeneration == generation
}

func (s *Store) refreshWatcherPaths(watcher *fsnotify.Watcher, generation uint64) {
	if !s.watcherCurrent(watcher, generation) {
		return
	}
	for _, root := range s.roots() {
		for _, dir := range watchDirectories(root.Dir, s.maxDepth) {
			_ = watcher.Add(dir)
		}
	}
}

// watchDirectories includes every existing directory that discovery can visit.
// For a missing root it subscribes to the nearest existing ancestor, allowing
// later creation to invalidate the snapshot. Symlink targets are traversed once.
func watchDirectories(root string, maxDepth int) []string {
	root = filepath.Clean(root)
	probe := root
	for {
		info, err := os.Stat(probe)
		if err == nil && info.IsDir() {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return nil
		}
		probe = parent
	}
	if probe != root {
		return []string{probe}
	}
	type pendingDir struct {
		path  string
		depth int
	}
	pending := []pendingDir{{path: root, depth: 0}}
	seen := map[string]bool{}
	var out []string
	for len(pending) > 0 {
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
			child := filepath.Join(current.path, entry.Name())
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
	return out
}

// Invalidate advances the catalog generation. The last complete snapshot stays
// available to cancelled callers until a replacement scan completes.
