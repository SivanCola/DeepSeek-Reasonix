package historycatalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reasonix/internal/base/backgroundwork"
	"reasonix/internal/state/sessionstore"
	"reasonix/internal/state/store"
	"slices"
)

type rootReconcileScan struct {
	root       Root
	paths      []string
	signature  string
	generation int64
	started    int64
	indexed    int
}

func (c *Catalog) reconcileRoot(ctx context.Context, root Root) error {
	var scan *rootReconcileScan
	err := c.scheduler().Do(ctx, backgroundwork.Rebuild, func(job context.Context) error {
		var err error
		scan, err = c.startRootScan(job, root)
		return err
	})
	if err != nil || scan == nil {
		return err
	}
	for _, path := range scan.paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.indexPending(ctx, root, scan.generation)
		if err := c.indexPath(ctx, root, path, scan.generation, -1); err == nil {
			scan.indexed++
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.scheduler().Do(ctx, backgroundwork.Rebuild, func(job context.Context) error { return c.finishRootScan(job, scan) })
}

func (c *Catalog) startRootScan(ctx context.Context, root Root) (*rootReconcileScan, error) {
	entries, err := os.ReadDir(root.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		c.setError(err)
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.IsDir() || !store.IsSessionTranscriptName(entry.Name()) ||
			store.IsSubagentTranscriptName(entry.Name()) {
			continue
		}
		path := filepath.Join(root.Path, entry.Name())
		if !root.Archive && !sessionstore.IsVisibleSession(path) {
			continue
		}
		paths = append(paths, path)
	}
	slices.Sort(paths)
	signature := historyRootSignature(paths)
	var previousSig, previousState string
	var previousIndexed, previousTotal int64
	_ = c.db.QueryRowContext(ctx, `SELECT signature,state,indexed,total FROM history_roots WHERE path=?`, root.Path).
		Scan(&previousSig, &previousState, &previousIndexed, &previousTotal)
	// Skip a completed, fully healthy directory: a corrupt/stale/missing source
	// is retry work the size/mtime signature cannot see. Healthy roots still
	// short-circuit before any transcript is opened.
	if previousState == "ready" && previousSig == signature && signature != "" &&
		previousIndexed == previousTotal && c.rootProjectionHealthy(ctx, root.Path) {
		return nil, nil
	}
	now := c.opts.Now().UnixMilli()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	var generation int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO history_roots(path,source,scope,workspace_root,signature,scan_generation,state,total)
        VALUES(?,?,?,?,?,1,'scanning',?) ON CONFLICT(path) DO UPDATE SET source=excluded.source,scope=excluded.scope,
        workspace_root=excluded.workspace_root,signature=excluded.signature,scan_generation=history_roots.scan_generation+1,state='scanning',error='',total=excluded.total
        RETURNING scan_generation`, root.Path, root.Source, root.Scope, root.WorkspaceRoot, signature, len(paths)).Scan(&generation); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &rootReconcileScan{root: root, paths: paths, signature: signature, generation: generation, started: now}, nil
}

func (c *Catalog) finishRootScan(ctx context.Context, scan *rootReconcileScan) error {
	root, generation, now, signature, indexed := scan.root, scan.generation, scan.started, scan.signature, scan.indexed
	live, absent, err := c.measureUnseen(ctx, root, generation)
	if err != nil {
		return err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := c.applyScanResult(ctx, tx, now, generation, live, absent); err != nil {
		_ = tx.Rollback()
		return err
	}
	cutoff := now - c.opts.MissingGrace.Milliseconds()
	if _, err := tx.ExecContext(ctx, `DELETE FROM history_fts WHERE rowid IN (
        SELECT d.id FROM history_documents d JOIN history_sources s ON s.path=d.source_path
        WHERE s.root=? AND s.seen_generation<>? AND s.missing_since>0 AND s.missing_since<=?
    )`, root.Path, generation, cutoff); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM history_documents WHERE source_path IN (
        SELECT path FROM history_sources WHERE root=? AND seen_generation<>? AND missing_since>0 AND missing_since<=?
    )`, root.Path, generation, cutoff); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM history_sources
        WHERE root=? AND seen_generation<>? AND missing_since>0 AND missing_since<=?`, root.Path, generation, cutoff); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE history_roots SET state='ready',signature=?,indexed=?,completed_at=?,scan_cursor='' WHERE path=?`, signature, indexed, now, root.Path); err != nil {
		_ = tx.Rollback()
		return err
	}
	revision, err := bump(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	c.publish(revision, []string{root.Path}, "reconcile")
	return nil
}
