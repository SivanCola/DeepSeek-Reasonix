package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reasonix/internal/fileutil"
)

const maxBackupsPerType = 5

// backupBeforeWrite copies the file at path to ~/.reasonix/backups/config/<name>-<timestamp>
// before it is overwritten. An absent file is silently skipped.
func backupBeforeWrite(path string) error {
	dir := BackupDir()
	if dir == "" {
		return nil
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	ts := time.Now().UTC().Format("20060102T150405")
	backupPath := filepath.Join(dir, fmt.Sprintf("%s-%s%s", stem, ts, ext))

	if err := os.WriteFile(backupPath, data, 0o600); err != nil {
		return err
	}
	rotateBackups(filepath.Join(dir, stem), ext)
	return nil
}

// rotateBackups keeps only the most recent maxBackupsPerType backups matching the
// stem-* pattern.
func rotateBackups(prefix, ext string) {
	pattern := filepath.Base(prefix) + "-*" + ext
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(prefix), pattern))
	if err != nil || len(matches) <= maxBackupsPerType {
		return
	}
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))
	for _, m := range matches[maxBackupsPerType:] {
		os.Remove(m)
	}
}

// writeConfigFile writes a config file atomically, with a backup of the previous
// version. Parent directories are created as needed.
func writeConfigFile(path, body string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("save: empty config path")
	}
	if err := backupBeforeWrite(path); err != nil {
		// Nonfatal: if backup fails, still try to save.
		_ = err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("save: create dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".reasonix.*.toml.tmp")
	if err != nil {
		return fmt.Errorf("save: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("save: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("save: close temp: %w", err)
	}
	return fileutil.ReplaceFile(tmpPath, path)
}
