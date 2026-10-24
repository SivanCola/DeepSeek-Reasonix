// roots.go — the one stats root the projection projects.
package usagecatalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ReconcileDir indexes dir, the projection's one authoritative stats root, and
// forgets every indexed file outside it. A relocated root (Settings → Storage
// move or adopt) restarts with the stats directory at a new absolute path;
// keeping the old one would sum every shared day twice on every launch. Files
// under dir that are no longer listed are forgotten too.
func (c *Catalog) ReconcileDir(ctx context.Context, dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	present := make(map[string]bool, len(entries))
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		present[absoluteClean(path)] = true
		paths = append(paths, path)
	}
	// Forgetting comes first: if this pass is interrupted, the days it pruned
	// read as unindexed (Ready falls back to JSONL) rather than as complete
	// with the old root's rows still folded in.
	if err := c.forgetOutsideRoot(ctx, dir, present); err != nil {
		return err
	}
	for _, path := range paths {
		if err := c.ReconcileFile(ctx, path, strings.TrimSuffix(filepath.Base(path), ".jsonl")); err != nil {
			return err
		}
	}
	return nil
}

// forgetOutsideRoot drops every indexed file that is not one of the current
// root's present files, then rederives the days that lost rows. The scan is
// over usage_files (one row per day file) and deletes are by primary key, so
// the pass is bounded by indexed files, not records. A moved-away root can
// still exist, as an adopt leaves it, and contributes nothing either way.
func (c *Catalog) forgetOutsideRoot(ctx context.Context, root string, present map[string]bool) error {
	rows, err := c.db.QueryContext(ctx, `SELECT path,day FROM usage_files`)
	if err != nil {
		return err
	}
	var stale []string
	days := map[string]bool{}
	rootAbs := absoluteClean(root)
	for rows.Next() {
		var path, day string
		if err := rows.Scan(&path, &day); err != nil {
			rows.Close()
			return err
		}
		if underRoot(rootAbs, path) && present[absoluteClean(path)] {
			continue
		}
		stale = append(stale, path)
		days[day] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(stale) == 0 {
		return nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, path := range stale {
		if _, err := tx.ExecContext(ctx, `DELETE FROM usage_records WHERE file_path=?`, path); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM usage_files WHERE path=?`, path); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	affected := make([]string, 0, len(days))
	for day := range days {
		affected = append(affected, day)
	}
	if err := rederiveDays(ctx, tx, affected); err != nil {
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
	c.revision.Store(revision)
	c.refresh(context.Background())
	return nil
}

// absoluteClean is the form indexed paths are compared in, so a root spelled
// relative or with a trailing separator still names the same directory.
func absoluteClean(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

// underRoot reports whether path is a file directly or indirectly inside root.
// A gone root has no files under it, which is what makes a vanished stats
// directory contribute nothing.
func underRoot(root, path string) bool {
	rel, err := filepath.Rel(root, absoluteClean(path))
	if err != nil {
		return false
	}
	return rel != "." && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
