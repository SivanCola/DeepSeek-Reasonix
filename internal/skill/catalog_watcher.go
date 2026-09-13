package skill

import (
	"os"
	"path/filepath"
	"sort"

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
	watcher, done := s.watcher, s.watcherDone
	s.watcher, s.watcherDone = nil, nil
	s.watcherMu.Unlock()
	s.catalogMu.Lock()
	if s.catalogFlight != nil && s.catalogFlight.cancel != nil {
		s.catalogFlight.cancel()
	}
	s.catalogMu.Unlock()
	if watcher != nil {
		_ = watcher.Close()
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
	if s.closed || s.watcher != nil {
		s.watcherMu.Unlock()
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
	s.watcher, s.watcherDone = watcher, done
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
