package hashline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/tool"
	"reasonix/internal/tool/builtin"
)

const readDefaultLimit = 2000

// Read is the hashline_read tool. Construct via NewRead; do not init-register.
type Read struct {
	workDir     string
	paths       *builtin.PathResolver
	forbidRoots []string
	overlay     builtin.FileOverlay
}

// NewRead returns a configured hashline_read tool.
func NewRead(workDir string, paths *builtin.PathResolver, forbidRoots []string, overlay builtin.FileOverlay) *Read {
	return &Read{
		workDir:     workDir,
		paths:       paths,
		forbidRoots: builtin.HashlineRealRoots(forbidRoots),
		overlay:     overlay,
	}
}

func (Read) Name() string { return "hashline_read" }

func (Read) Description() string {
	return "Read a text file with Hashline v1 anchors on each line (LINE:LOCAL:CHUNK→CONTENT). " +
		"Pass anchors to hashline_edit for validated edits. Use offset/limit to page large files; " +
		"anchors always use full-file chunk context. Anchors are valid only for the file state at read time."
}

func (Read) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "path":{"type":"string","description":"File path"},
  "offset":{"type":"integer","description":"0-based line offset to start reading from (default 0)","minimum":0},
  "limit":{"type":"integer","description":"Maximum lines to return (default 2000)","minimum":1}
},
"required":["path"]
}`)
}

func (Read) ReadOnly() bool { return true }

func (Read) SnipHint() tool.SnipHint {
	return tool.SnipHint{Head: 120, Tail: 12, HeadChars: 12000, TailChars: 2000}
}

// PermissionAlias documents the classic tool this maps to for policy authors.
// The tool reports Name()=hashline_read; permission rules match that name (or
// bare reader-allow). There is no runtime alias interface today.
func (Read) PermissionAlias() string { return "read_file" }

func (r Read) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if p.Offset < 0 {
		p.Offset = 0
	}
	if p.Limit <= 0 {
		p.Limit = readDefaultLimit
	}

	rp := builtin.HashlineResolveReadable(r.workDir, p.Path, r.paths)
	path := rp.Path
	display := rp.DisplayPath
	if builtin.HashlineConfineRead(r.forbidRoots, path) {
		err := &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
		if rp.External {
			return "", fmt.Errorf("read %s: %s", display, rp.ErrorText(err))
		}
		return "", err
	}

	// Overlay (unsaved editor buffers) first for non-external absolute paths.
	if r.overlay != nil && !rp.External && filepath.IsAbs(path) {
		if content, ok := r.overlay.ReadTextFile(ctx, path); ok {
			return formatReadResult(content, p.Offset, p.Limit), nil
		}
	}

	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return "", fmt.Errorf("%s is a directory, not a file — use the ls tool to list it, or read a specific file inside it", display)
	}

	content, err := readTextFile(path)
	if err != nil {
		if rp.External {
			return "", fmt.Errorf("read %s: %s", display, rp.ErrorText(err))
		}
		return "", fmt.Errorf("read %s: %w", display, err)
	}
	return formatReadResult(content, p.Offset, p.Limit), nil
}

func formatReadResult(content string, offset, limit int) string {
	if content == "" {
		return FormatHashlineContent("", 0, 1) + "\n\n(empty file)"
	}
	lines := SplitLines(content)
	total := len(lines)
	if offset >= total {
		return fmt.Sprintf("(offset %d is past EOF — file has %d lines)", offset, total)
	}
	window := FormatHashlineContent(content, offset, limit)
	var trailer strings.Builder
	if offset+limit < total {
		fmt.Fprintf(&trailer, "\n\n[more lines below; pass offset=%d to continue; total_lines=%d]", offset+limit, total)
	} else if offset > 0 {
		fmt.Fprintf(&trailer, "\n\n[total_lines=%d]", total)
	}
	return window + trailer.String()
}

func readTextFile(path string) (string, error) {
	content, _, err := builtin.HashlineReadEncoded(path)
	if err != nil {
		return "", err
	}
	// Reject binary: NUL after decode (UTF-16 already decoded).
	if strings.IndexByte(content, 0) >= 0 {
		return "", fmt.Errorf("binary file %s (NUL byte detected); not shown by hashline_read", path)
	}
	return content, nil
}
