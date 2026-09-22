package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/fileops"
	"reasonix/internal/store"
)

// One preparation owns a source/head generation. Readers own references,
// rather than independently rebuilding the same derived SQLite index.
type nativeHistoryPreparation struct {
	key      string
	cacheKey string
	refs     int // guarded by desktopHistoryReaders.mu
	done     chan struct{}
	closed   chan struct{}
	cancel   context.CancelFunc
	ctx      context.Context
	pager    *agent.DisplayPager
	err      error
}

// Stat identities are task admission tokens, not content proof. OpenDisplayPager
// independently validates the selected branch and authoritative content.
func nativeHistorySourceGeneration(path string) (string, error) {
	hash := sha256.New()
	for i, candidate := range []string{path, store.SessionEventLog(path), store.SessionDisplayIndex(path)} {
		info, err := os.Stat(candidate)
		if i > 0 && os.IsNotExist(err) {
			fmt.Fprintf(hash, "%d:absent\x00", i)
			continue
		}
		if err != nil {
			return "", err
		}
		target, version := fileops.DiskSnapshot(candidate, info)
		fmt.Fprintf(hash, "%d:%s:%s\x00", i, target.Key, version)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func (p *nativeHistoryPreparation) wait(ctx context.Context) (*agent.DisplayPager, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return p.pager, p.err
	}
}

// Caller holds historyReaders.mu. All disk work runs outside both this mutex
// and App.mu. Its lifetime never owns model execution or persistence.
func (a *App) acquireNativeHistoryLocked(path, head, sourceKey, generation string) (*nativeHistoryPreparation, context.CancelFunc) {
	manager := &a.historyReaders
	if manager.native == nil {
		manager.native = map[string]*nativeHistoryPreparation{}
	}
	sum := sha256.Sum256([]byte(sourceKey + "\x00" + head))
	cacheKey := fmt.Sprintf("%x", sum)
	key := cacheKey + ":" + generation
	job := manager.native[key]
	if job == nil {
		// A replaced source invalidates its old readers. Retire those derived
		// handles before replacing the shared cache, including on Windows.
		var retired []<-chan struct{}
		for _, previous := range manager.native {
			if previous.cacheKey == cacheKey {
				previous.cancel()
				retired = append(retired, previous.closed)
			}
		}
		ctx, cancel := context.WithCancel(a.bootContext())
		ctx = a.historyMaintenance.Context(ctx)
		job = &nativeHistoryPreparation{key: key, cacheKey: cacheKey, done: make(chan struct{}), closed: make(chan struct{}), cancel: cancel, ctx: ctx}
		manager.native[key] = job
		manager.workers.Add(1)
		go func() {
			defer manager.workers.Done()
			defer close(job.closed)
			for _, closed := range retired {
				select {
				case <-closed:
				case <-ctx.Done():
					job.err = ctx.Err()
					close(job.done)
					return
				}
			}
			release, err := a.historyMaintenance.Foreground(ctx)
			if err == nil {
				root := config.CacheDir()
				current, verifyErr := nativeHistorySourceGeneration(path)
				if verifyErr != nil {
					err = verifyErr
				} else if current != generation {
					err = agent.ErrDisplaySourceChanged
				} else if root == "" {
					err = fmt.Errorf("history cache unavailable")
				} else {
					job.pager, err = agent.OpenDisplayPager(ctx, path, filepath.Join(root, "history-display-v1", cacheKey+".sqlite"), head)
					if err == nil {
						current, verifyErr = nativeHistorySourceGeneration(path)
						if verifyErr != nil {
							err = verifyErr
						} else if current != generation {
							err = agent.ErrDisplaySourceChanged
						}
					}
				}
				release()
			}
			job.err = err
			close(job.done)
			<-ctx.Done()
			if job.pager != nil {
				_ = job.pager.Close()
			}
		}()
	}
	job.refs++
	var once sync.Once
	return job, func() {
		once.Do(func() {
			manager.mu.Lock()
			job.refs--
			if job.refs == 0 {
				if manager.native[key] == job {
					delete(manager.native, key)
				}
				job.cancel()
			}
			manager.mu.Unlock()
		})
	}
}
