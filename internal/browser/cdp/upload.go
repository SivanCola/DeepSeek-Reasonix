package cdp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/sandbox"
)

// uploadRoots decides which files browser_upload may hand to a page. A page is
// untrusted: an unrestricted file input would let any site the agent visits
// receive any file this process can read, so a path outside the session's own
// roots is refused. The tool promises the model that only files the task owns
// can be attached, and this is where that promise is kept.
type uploadRoots struct {
	roots []string
}

// newUploadRoots resolves the session's roots plus the executor's artifact
// directory, so a file the agent just downloaded can be attached too. Roots
// are symlink-resolved once here because candidates are compared after their
// own symlinks are resolved.
func newUploadRoots(configured []string, artifacts string) uploadRoots {
	resolved := make([]string, 0, len(configured)+1)
	for _, root := range append(append([]string{}, configured...), artifacts) {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			abs = real
		}
		resolved = append(resolved, filepath.Clean(abs))
	}
	return uploadRoots{roots: resolved}
}

// resolve returns the real path of an upload candidate, or the reason the
// model cannot attach it. Symlinks are resolved before the containment check,
// so a link inside the workspace cannot point a file input at a private key.
func (u uploadRoots) resolve(path string) (string, string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Sprintf("file %s: %v", path, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Sprintf("file %s is not readable: %v", path, err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", fmt.Sprintf("file %s is not readable: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Sprintf("%s is not a regular file", path)
	}
	for _, root := range u.roots {
		if sandbox.PathWithin(root, real) {
			return real, ""
		}
	}
	return "", fmt.Sprintf("file %s is outside this task's directories, so it cannot be attached to a page", path)
}
