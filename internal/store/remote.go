package store

import (
	"fmt"
	"hash/fnv"
	"strings"
	"unicode/utf8"
)

// Remote-SSH module naming: the canonical file names for the state a
// bootstrapped remote serve leaves under the remote host's
// ~/.reasonix/remote/. Only name derivation lives here (this package is the
// path authority and does no I/O); reads and writes happen over SFTP in
// internal/remote. Local-side absolute paths (managed known_hosts) are
// derived in internal/config/paths.go, which owns REASONIX_HOME resolution.

// RemoteDirName is the directory under the remote ~/.reasonix that holds all
// remote-module state, and under the local Reasonix home that holds the
// managed known_hosts file.
const RemoteDirName = "remote"

// RemoteBinDirName holds an uploaded reasonix binary on the remote host:
// ~/.reasonix/remote/bin/reasonix.
const RemoteBinDirName = "bin"

// RemoteWorkspaceSlug flattens a remote (POSIX) workspace path into a
// filename component, mirroring config.WorkspaceSlug's shape. Remote targets
// are Linux/macOS only, so no case folding applies; overlong results are
// truncated with an FNV-1a suffix so distinct deep paths cannot collide.
func RemoteWorkspaceSlug(remotePath string) string {
	slug := strings.NewReplacer("/", "-", ":", "-").Replace(strings.TrimSuffix(remotePath, "/"))
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "root"
	}
	return boundRemoteComponent(slug, 200) // room for the serve-<slug>.token wrapper
}

// RemoteServeStateName is the per-workspace serve state JSON: pid, addr,
// workspace, version, started_at.
func RemoteServeStateName(slug string) string { return "serve-" + slug + ".json" }

// RemoteServeTokenName holds the pre-shared auth token (0600), written over
// SFTP before launch and read by serve via --token-file.
func RemoteServeTokenName(slug string) string { return "serve-" + slug + ".token" }

// RemoteServeLogName captures the detached serve's stdout/stderr.
func RemoteServeLogName(slug string) string { return "serve-" + slug + ".log" }

// RemoteServePortName receives the real bound address via --port-file.
func RemoteServePortName(slug string) string { return "serve-" + slug + ".port" }

// RemoteServePidName receives the server pid via --pid-file.
func RemoteServePidName(slug string) string { return "serve-" + slug + ".pid" }

// boundRemoteComponent mirrors config.boundFilenameComponent (this package is
// a stdlib-only leaf and cannot import config): inputs at or under the budget
// pass through byte-identical; longer ones are truncated at a rune boundary
// with an FNV-1a hash of the full input appended.
func boundRemoteComponent(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	budget := maxLen - 17 // "-" + 16 hex digits
	prefix := s[:budget]
	for len(prefix) > 0 && !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	return fmt.Sprintf("%s-%016x", prefix, h.Sum64())
}
