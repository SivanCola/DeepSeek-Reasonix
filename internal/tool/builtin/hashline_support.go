package builtin

import (
	"context"

	fileenc "reasonix/internal/fileutil/encoding"
	"reasonix/internal/sandbox"
	"reasonix/internal/tool"
)

// Helpers below are exported for the hashline subpackage so it can reuse the
// same path resolution, confinement, encoding, and search wiring as the classic
// built-ins without registering hashline tools into the global Builtin set.

// HashlineResolvePath resolves a model-supplied path against workDir.
func HashlineResolvePath(workDir, p string) string {
	return resolveIn(workDir, p)
}

// HashlineResolveReadable resolves a path for a read/search tool, including
// session-scoped external read aliases.
func HashlineResolveReadable(workDir, path string, resolver *PathResolver) ResolvedPath {
	return resolveReadablePath(workDir, path, resolver)
}

// HashlineConfineRead reports whether target is forbidden for read tools.
func HashlineConfineRead(forbidRoots []string, target string) bool {
	return confineRead(forbidRoots, target)
}

// HashlineConfineWrite is the write-tool boundary check (workspace + session
// guard + managed-config approval).
func HashlineConfineWrite(ctx context.Context, roots []string, guard SessionDataGuard, managed ManagedConfigPaths, target string) error {
	return confineWrite(ctx, roots, guard, managed, target)
}

// HashlineConfinePreview mirrors confinePreview for hashline_edit Previewer:
// same writable-root / session-guard / managed-config visibility checks as
// Execute, without the managed-config approval prompt.
func HashlineConfinePreview(roots []string, guard SessionDataGuard, managed ManagedConfigPaths, target string) error {
	return confinePreview(roots, guard, managed, target)
}

// HashlineRealRoots resolves write/forbid roots the same way classic tools do.
func HashlineRealRoots(roots []string) []string {
	return realRoots(roots)
}

// HashlineReadEncoded reads and decodes a file, preserving encoding kind for
// a subsequent write-back.
func HashlineReadEncoded(path string) (content string, enc fileenc.Kind, err error) {
	return readFileEncoded(path)
}

// HashlineWriteEncoded re-encodes content and writes it, preserving charset/BOM.
func HashlineWriteEncoded(path, content string, enc fileenc.Kind) error {
	return writeFileEncoded(path, content, enc)
}

// NewGrepTool constructs a configured grep tool for post-processing (hashline_grep).
func NewGrepTool(workDir string, paths *PathResolver, spec SearchSpec, sb sandbox.Spec, forbidRoots []string) tool.Tool {
	return grepTool{
		workDir:     workDir,
		paths:       paths,
		rg:          spec.RgPath,
		forbidRoots: realRoots(forbidRoots),
		sb:          sb,
	}
}
