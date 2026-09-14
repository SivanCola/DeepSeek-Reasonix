package main

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// One parser for every local path the renderer may name: Markdown image
// sources, chat file references, and their file:// URL spellings. Path
// resolution follows the host that owns the file, so `filepath.IsAbs` — and
// therefore the Windows drive/UNC rules — is deliberately the local OS's.
func localPathSource(source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" || strings.ContainsRune(source, 0) {
		return "", os.ErrInvalid
	}
	// Raw Windows drive/UNC paths are valid sources even though net/url would
	// otherwise interpret the drive letter as a URL scheme.
	if filepath.IsAbs(source) && !strings.ContainsAny(source, "?#") {
		return filepath.Clean(source), nil
	}
	if strings.HasPrefix(strings.ToLower(source), "file:") {
		u, err := url.Parse(source)
		if err != nil || !strings.EqualFold(u.Scheme, "file") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return "", os.ErrInvalid
		}
		if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
			return "", os.ErrPermission
		}
		path, err := url.PathUnescape(u.Path)
		if err != nil {
			return "", os.ErrInvalid
		}
		if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
			path = path[1:]
		}
		return filepath.FromSlash(path), nil
	}
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "" {
		return "", os.ErrInvalid
	}
	path, err := url.PathUnescape(u.Path)
	if err != nil || strings.ContainsRune(path, 0) {
		return "", os.ErrInvalid
	}
	return filepath.FromSlash(path), nil
}

// canonicalPathWithin resolves symlinks on both sides before comparing, so a
// link inside the root cannot be used to reach a file outside it.
//
// It returns the resolved root as well as the resolved candidate: a caller that
// derives a display path must measure from the same resolved root, or a
// symlinked workspace (macOS /tmp, a linked home) would produce a ../ chain
// instead of the relative path the file tree uses.
func canonicalPathWithin(root, candidate string) (resolved, resolvedRoot string, err error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", err
	}
	realCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(realRoot, realCandidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", "", os.ErrPermission
	}
	return filepath.Clean(realCandidate), filepath.Clean(realRoot), nil
}

func localFileHref(path string) string {
	slash := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && len(slash) >= 2 && slash[1] == ':' {
		slash = "/" + slash
	}
	return (&url.URL{Scheme: "file", Path: slash}).String()
}
