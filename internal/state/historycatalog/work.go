package historycatalog

import (
	"context"

	"reasonix/internal/base/backgroundwork"
)

func (c *Catalog) scheduler() *backgroundwork.Scheduler {
	if c.opts.Work != nil {
		return c.opts.Work
	}
	return backgroundwork.Shared
}

func (c *Catalog) indexPath(ctx context.Context, root Root, path string, generation int64, appendFrom int) error {
	priority := backgroundwork.Rebuild
	if generation == 0 {
		priority = backgroundwork.Incremental
	}
	return c.scheduler().Do(ctx, priority, func(job context.Context) error {
		return c.indexPathOnce(job, root, path, generation, appendFrom)
	})
}

func (c *Catalog) indexPending(ctx context.Context, scanning Root, generation int64) bool {
	select {
	case path := <-c.queue:
		c.mu.Lock()
		queued, ok := c.paths[path]
		delete(c.paths, path)
		c.mu.Unlock()
		if !ok {
			return true
		}
		if queued.root.Path != scanning.Path {
			generation = 0
		}
		_ = c.scheduler().Do(ctx, backgroundwork.Incremental, func(job context.Context) error {
			return c.indexPathOnce(job, queued.root, path, generation, queued.appendFrom)
		})
		return true
	default:
		return false
	}
}

func (c *Catalog) ensureTokenizerVersion(ctx context.Context) error {
	return c.scheduler().Do(ctx, backgroundwork.Rebuild, c.ensureTokenizerVersionOnce)
}

func (c *Catalog) sourceReadError(ctx context.Context, root Root, path string, generation int64, contentFingerprint, metaFingerprint string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	_, _ = c.db.ExecContext(ctx, `INSERT INTO history_sources(path,root,source,scope,workspace_root,content_fingerprint,meta_fingerprint,health,last_error,seen_generation)
            VALUES(?,?,?,?,?,?,?,'corrupt',?,?) ON CONFLICT(path) DO UPDATE SET health='corrupt',last_error=excluded.last_error,
            content_fingerprint=excluded.content_fingerprint,meta_fingerprint=excluded.meta_fingerprint,seen_generation=excluded.seen_generation`,
		path, root.Path, root.Source, root.Scope, root.WorkspaceRoot, contentFingerprint, metaFingerprint, err.Error(), generation)
	c.setError(err)
	return err
}
