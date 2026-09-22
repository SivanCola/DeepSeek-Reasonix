package sessioncatalog

import (
	"context"
	"errors"
	"os"
	"time"

	"reasonix/internal/historywork"
)

type metadataQueueJob struct {
	target   DirectoryTarget
	scan     *metadataScan
	ready    time.Time
	turn     uint64
	failures int
}

// The queue retains each directory iterator, but admits only one slice. A
// large root therefore cannot prevent another root from publishing its first
// batch. Committed path updates use the independent small-metadata writer.
func (c *Catalog) metadataReconcileLoop() {
	jobs := map[string]*metadataQueueJob{}
	defer func() {
		for _, job := range jobs {
			if job.scan != nil {
				job.scan.close(c.workerCtx, context.Canceled)
			}
		}
	}()
	var turn uint64
	for c.workerCtx.Err() == nil {
		c.reconcileDirtyMu.Lock()
		c.reconcileQueued.Range(func(key, value any) bool {
			id := key.(string)
			if jobs[id] == nil {
				jobs[id] = &metadataQueueJob{target: value.(DirectoryTarget)}
				delete(c.reconcileDirty, id)
			}
			return true
		})
		c.reconcileDirtyMu.Unlock()
		var selected string
		var next *metadataQueueJob
		now := c.opts.Now()
		for key, job := range jobs {
			if job.ready.After(now) {
				continue
			}
			priority := c.isPriorityDirectory(job.target)
			if !priority && c.opts.Maintenance != nil && c.opts.Maintenance.ForegroundActive() {
				continue
			}
			if next == nil || priority && !c.isPriorityDirectory(next.target) || priority == c.isPriorityDirectory(next.target) && (job.turn < next.turn || job.turn == next.turn && job.target.mutationSeq < next.target.mutationSeq) {
				selected, next = key, job
			}
		}
		if next == nil {
			timer := time.NewTimer(historywork.PauseDuration)
			select {
			case <-c.stop:
				timer.Stop()
				return
			case <-c.reconcileCh:
				timer.Stop()
			case <-timer.C:
			}
			continue
		}
		turn++
		next.turn = turn
		var err error
		if next.scan == nil {
			if c.testReconcileStartHook != nil {
				c.testReconcileStartHook(next.target)
			}
			next.scan, err = c.startMetadataScan(c.workerCtx, next.target, next.target.mutationSeq, true)
		}
		var done bool
		var bytes int64
		if err == nil {
			done, bytes, err = next.scan.step(c.workerCtx)
		}
		if errors.Is(err, errMetadataScanBusy) || errors.Is(err, historywork.ErrForegroundActive) {
			next.ready = now.Add(historywork.PauseDuration)
			continue
		}
		if done || err != nil {
			if next.scan != nil {
				next.scan.close(c.workerCtx, err)
				next.scan = nil
			}
			if done && err == nil {
				c.settleReconcileTarget(next.target)
			}
			c.reconcileDirtyMu.Lock()
			if follow, dirty := c.reconcileDirty[selected]; dirty {
				delete(c.reconcileDirty, selected)
				next.target, next.failures, next.ready = follow, 0, time.Time{}
			} else if err != nil && !errors.Is(err, context.Canceled) && !os.IsNotExist(err) && !os.IsPermission(err) && next.failures < 3 {
				next.ready = c.opts.Now().Add([]time.Duration{time.Second, 5 * time.Second, 30 * time.Second}[next.failures])
				next.failures++
			} else {
				c.reconcileQueued.Delete(selected)
				delete(jobs, selected)
				if done := c.reconcileDone[selected]; done != nil {
					delete(c.reconcileDone, selected)
					close(done)
				}
			}
			c.reconcileDirtyMu.Unlock()
		}
		// Apply a global pause, including when the next slice belongs to a
		// different root. Per-root timers alone multiply the allowed I/O rate.
		if c.opts.Maintenance == nil {
			if err := historywork.Pause(c.workerCtx, bytes); err != nil {
				return
			}
		}
	}
}

func (c *Catalog) PrioritizeWorkspace(scope, root string) {
	scope, root = normalizeScope(scope, root)
	c.priorityWorkspace.Store(scope + "\x00" + c.workspaceRootKey(scope, root))
}

func (c *Catalog) isPriorityDirectory(target DirectoryTarget) bool {
	scope, root := normalizeScope(target.Scope, target.WorkspaceRoot)
	key, _ := c.priorityWorkspace.Load().(string)
	return key == scope+"\x00"+c.workspaceRootKey(scope, root)
}
