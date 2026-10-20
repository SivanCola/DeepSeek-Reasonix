package agentpreset

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reasonix/internal/base/fileutil"
	"reasonix/internal/base/secrets"
	"reasonix/internal/contract/agentcomposition"
)

type resourceCapture struct {
	root     *os.Root
	path     string
	snapshot DirectorySnapshot
	bytes    int
	entries  int
	active   map[string]bool
}

// CaptureDirectory flattens links confined to the source tree into ordinary files.
// Retained directories use a separate strict reader so links cannot mask tampering.
func CaptureDirectory(root string) (DirectorySnapshot, error) {
	path, err := fileutil.ResolveExistingPath(root)
	if err != nil {
		return DirectorySnapshot{}, resourceReadError(filepath.Base(root), err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return DirectorySnapshot{}, err
	}
	dir, err := os.OpenRoot(path)
	if err != nil {
		return DirectorySnapshot{}, resourceReadError(filepath.Base(root), err)
	}
	defer dir.Close()
	c := resourceCapture{root: dir, path: path, snapshot: DirectorySnapshot{Files: []AssetFile{}}, active: map[string]bool{}}
	err = c.visit(".", ".")
	if err != nil && !errors.Is(err, agentcomposition.ErrUnavailable) {
		err = resourceReadError(filepath.Base(root), err)
	}
	sort.Slice(c.snapshot.Files, func(i, j int) bool { return c.snapshot.Files[i].Path < c.snapshot.Files[j].Path })
	return c.snapshot, err
}

func vcsResource(name string) bool {
	return name == ".git" || name == ".hg" || name == ".svn"
}

type resourceCaptureFailure struct{ path, reason string }

func (e *resourceCaptureFailure) Error() string {
	return fmt.Sprintf("%s: resource %s: %s", agentcomposition.ErrUnavailable, e.path, e.reason)
}
func (e *resourceCaptureFailure) Unwrap() error           { return agentcomposition.ErrUnavailable }
func (e *resourceCaptureFailure) DiagnosticFacts() string { return e.Error() }

func resourceCaptureError(path, reason string) error {
	return &resourceCaptureFailure{path: path, reason: reason}
}

func resourceReadError(path string, err error) error {
	reason := "resource cannot be read"
	if errors.Is(err, os.ErrPermission) {
		reason = "permission denied"
	} else if errors.Is(err, os.ErrNotExist) {
		reason = "resource no longer exists"
	}
	return errors.Join(resourceCaptureError(path, reason), err)
}

func (c *resourceCapture) resolve(path string) (string, error) {
	resolved, err := fileutil.ResolveExistingPath(filepath.Join(c.path, path))
	if err != nil {
		return "", resourceReadError(path, err)
	}
	rel, err := filepath.Rel(c.path, resolved)
	if err != nil || !filepath.IsLocal(rel) {
		return "", resourceCaptureError(path, "link leaves the source directory")
	}
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		if secrets.SensitiveFileName(part) || vcsResource(part) {
			return "", resourceCaptureError(path, "link targets credential or VCS data")
		}
	}
	return rel, nil
}

func (c *resourceCapture) visit(path, output string) error {
	c.entries++
	if c.entries > maxAssetFiles || len(c.active) >= 256 {
		return resourceCaptureError(output, "resource limit exceeded")
	}
	if output != "." && secrets.SensitiveFileName(filepath.Base(output)) {
		return resourceCaptureError(output, "resource requires an external credential reference")
	}
	source, err := c.resolve(path)
	if err != nil {
		return err
	}
	info, err := c.root.Lstat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return resourceCaptureError(output, "unsupported file type")
	}
	// Root.Open confines the actual read even if a link changes after resolution.
	file, err := c.root.Open(source)
	if err != nil {
		return resourceReadError(output, err)
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		if c.active[source] {
			return resourceCaptureError(output, "directory link cycle")
		}
		c.active[source] = true
		defer delete(c.active, source)
		entries, err := file.ReadDir(-1)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if vcsResource(entry.Name()) {
				continue
			}
			if err := c.visit(filepath.Join(source, entry.Name()), filepath.Join(output, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return resourceCaptureError(output, "unsupported file type")
	}
	remaining := maxAssetBytes - c.bytes
	if info.Size() > int64(remaining) {
		return resourceCaptureError(output, "resource limit exceeded")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(remaining)+1))
	if err != nil {
		return err
	}
	if len(data) > remaining {
		return resourceCaptureError(output, "resource limit exceeded")
	}
	c.bytes += len(data)
	c.snapshot.Files = append(c.snapshot.Files, AssetFile{Path: filepath.ToSlash(output), Data: data, Mode: uint32(info.Mode().Perm() & 0o700)})
	return nil
}
