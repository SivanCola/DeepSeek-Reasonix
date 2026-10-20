package scheduler

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"reasonix/internal/contract/config"
	"reasonix/internal/state/workspacelist"
)

// Folders are the project roots a schedule may read: the remembered projects,
// and current when it is one, that the person has marked trusted. The list
// comes from the person's own record, never from a request, and a request is
// only ever compared with it.
func Folders(current string) []string {
	roots := config.RootsForHome("")
	grants := config.NewProjectGrantStore(roots.Home())
	var candidates []string
	if dir := config.MemoryUserDir(); dir != "" {
		if list, err := workspacelist.Read(filepath.Join(dir, workspacelist.FileName)); err == nil {
			candidates = list.Paths
		}
	}
	if current != "" {
		candidates = append(candidates, current)
	}
	var out []string
	for _, p := range candidates {
		if trust, err := grants.Trust(p); err != nil || trust != config.WorkspaceTrusted || contains(out, p) {
			continue
		}
		// A project that is no longer there is not one a schedule can read, and
		// answering "gone" would tell a caller about paths it was never offered.
		if info, err := os.Stat(p); err != nil || !info.IsDir() {
			continue
		}
		out = append(out, p)
	}
	return out
}

func contains(list []string, p string) bool {
	for _, q := range list {
		if samePath(q, p) {
			return true
		}
	}
	return false
}

// Match returns the folder in folders that requested names, in the list's own
// spelling, and false when it names none of them. It compares the two paths as
// text, so a requested path is never followed or opened: a parent directory, a
// relative path, a path through "..", a missing folder and an untrusted project
// are all simply not in the list.
func Match(folders []string, requested string) (string, bool) {
	requested = strings.TrimSpace(requested)
	if requested == "" || !filepath.IsAbs(requested) {
		return "", false
	}
	for _, f := range folders {
		if samePath(f, requested) {
			return f, true
		}
	}
	return "", false
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
