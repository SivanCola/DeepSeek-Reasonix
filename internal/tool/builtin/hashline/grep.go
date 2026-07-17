package hashline

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"reasonix/internal/sandbox"
	"reasonix/internal/tool"
	"reasonix/internal/tool/builtin"
)

// Grep is the hashline_grep tool. Construct via NewGrep; do not init-register.
type Grep struct {
	workDir     string
	paths       *builtin.PathResolver
	forbidRoots []string
	underlying  tool.Tool // classic grep, configured the same way
}

// NewGrep returns a configured hashline_grep tool.
func NewGrep(workDir string, paths *builtin.PathResolver, spec builtin.SearchSpec, sb sandbox.Spec, forbidRoots []string) *Grep {
	return &Grep{
		workDir:     workDir,
		paths:       paths,
		forbidRoots: builtin.HashlineRealRoots(forbidRoots),
		underlying:  builtin.NewGrepTool(workDir, paths, spec, sb, forbidRoots),
	}
}

func (Grep) Name() string { return "hashline_grep" }

func (Grep) Description() string {
	return "Search file contents with Hashline v1 anchors on match lines " +
		"(LINE:LOCAL:CHUNK:content for matches). Anchors can be passed to hashline_edit " +
		"without an intermediate read. Pattern is RE2; path defaults to \".\"; " +
		"results capped like classic grep. Stale/unreadable files drop anchors for those lines."
}

func (Grep) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string","description":"Regular expression (RE2 syntax)"},"path":{"type":"string","description":"File or directory to search (default \".\")"},"timeout_seconds":{"type":"integer","description":"Abort and return partial matches after this many seconds (default 30, max 300).","minimum":1}},"required":["pattern"]}`)
}

func (Grep) ReadOnly() bool { return true }

func (Grep) SnipHint() tool.SnipHint {
	return tool.SnipHint{Head: 80, Tail: 8, HeadChars: 10000, TailChars: 1000}
}

// PermissionAlias documents the classic tool this maps to for policy authors.
func (Grep) PermissionAlias() string { return "grep" }

func (g Grep) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if g.underlying == nil {
		return "", fmt.Errorf("hashline_grep: not configured")
	}
	out, err := g.underlying.Execute(ctx, args)
	if err != nil {
		return "", err
	}
	// Inject anchors into path:line:text lines.
	return g.injectAnchors(ctx, out), nil
}

func (g Grep) injectAnchors(ctx context.Context, out string) string {
	if out == "" || strings.HasPrefix(out, "(no matches") {
		return out
	}
	cache := map[string][]Anchor{}
	linesSnap := map[string][]string{}
	var b strings.Builder
	for i, line := range strings.Split(out, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		// Trailer lines from formatGrep.
		if strings.HasPrefix(line, "...") || line == "" {
			b.WriteString(line)
			continue
		}
		path, lineNo, sep, rest, ok := parseGrepLine(line)
		if !ok {
			b.WriteString(line)
			continue
		}
		// Resolve path relative to workDir when needed.
		abs := path
		if !filepath.IsAbs(path) {
			abs = builtin.HashlineResolvePath(g.workDir, path)
		}
		if builtin.HashlineConfineRead(g.forbidRoots, abs) {
			b.WriteString(line) // drop anchors; keep original
			continue
		}
		anchors, ok := cache[abs]
		if !ok {
			if ctx.Err() != nil {
				b.WriteString(line)
				continue
			}
			content, err := readTextFile(abs)
			if err != nil {
				b.WriteString(line)
				continue
			}
			ls := SplitLines(content)
			anchors = GenerateAnchors(ls)
			cache[abs] = anchors
			linesSnap[abs] = ls
		}
		// Re-validate: if line number is out of range or content at that line
		// no longer matches rest (stale snapshot vs grep), drop anchors.
		if lineNo < 1 || lineNo > len(anchors) {
			b.WriteString(line)
			continue
		}
		// Soft content check: if rest is non-empty and differs from snap line
		// after whitespace-normalization mismatch of local hash, discard.
		snap := linesSnap[abs]
		if lineNo <= len(snap) {
			// Grep rest may include content as emitted; compare loosely.
			if rest != "" && snap[lineNo-1] != rest {
				// Still attach anchors if the line number exists — grep may
				// have truncated, but prefer validating local hash.
				local := EncodeHash(LineHash(snap[lineNo-1]), DefaultHashLen)
				if local != anchors[lineNo-1].Local {
					b.WriteString(line)
					continue
				}
			}
		}
		a := anchors[lineNo-1]
		// Match lines use ':' after the anchor (grep style); context would use '-'.
		fmt.Fprintf(&b, "%s:%d:%s%s%s", path, lineNo, a.Suffix(), string(sep), rest)
	}
	return b.String()
}

// parseGrepLine parses "path:line:text" (or path:line-text for context).
// Path may contain colons (Windows drive); we find the last ":digits:" pattern.
func parseGrepLine(line string) (path string, lineNo int, sep byte, rest string, ok bool) {
	// Scan for :digits: or :digits- from the left after first colon-ish path.
	// Reasonix native/rg output is path:line:text with absolute or relative path.
	// Strategy: find ":N:" where N is digits, taking the rightmost plausible
	// match that leaves a non-empty path.
	for i := 0; i < len(line); i++ {
		if line[i] != ':' {
			continue
		}
		j := i + 1
		if j >= len(line) || line[j] < '0' || line[j] > '9' {
			continue
		}
		for j < len(line) && line[j] >= '0' && line[j] <= '9' {
			j++
		}
		if j >= len(line) {
			continue
		}
		sepCand := line[j]
		if sepCand != ':' && sepCand != '-' {
			continue
		}
		// Prefer first valid path:line:text (paths rarely embed :digits:).
		n, err := strconv.Atoi(line[i+1 : j])
		if err != nil || n <= 0 {
			continue
		}
		path = line[:i]
		if path == "" {
			continue
		}
		return path, n, sepCand, line[j+1:], true
	}
	return "", 0, 0, "", false
}
