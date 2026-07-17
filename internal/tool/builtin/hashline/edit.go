package hashline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/diff"
	fileenc "reasonix/internal/fileutil/encoding"
	"reasonix/internal/tool"
	"reasonix/internal/tool/builtin"
)

// Edit is the hashline_edit tool. Construct via NewEdit; do not init-register.
type Edit struct {
	workDir string
	roots   []string
	guard   builtin.SessionDataGuard
	managed builtin.ManagedConfigPaths
	overlay builtin.FileOverlay
}

// NewEdit returns a configured hashline_edit tool.
func NewEdit(workDir string, roots []string, guard builtin.SessionDataGuard, managed builtin.ManagedConfigPaths, overlay builtin.FileOverlay) *Edit {
	return &Edit{
		workDir: workDir,
		roots:   builtin.HashlineRealRoots(roots),
		guard:   guard,
		managed: managed,
		overlay: overlay,
	}
}

func (Edit) Name() string { return "hashline_edit" }

func (Edit) Description() string {
	return `Edit a file using Hashline v1 anchors from hashline_read or hashline_grep.

Operations (use the "op" field):
  "replace" — Replace one line or a range (end_anchor inclusive). Empty content deletes.
  "insert_after" — Insert after anchor; "0:" = beginning of file; "EOF" = end of file.
  "write" — Replace entire file (must be the sole op).

Batch: pass multiple ops in "edits" (array, single object, or double-JSON string; max 64).
All anchors validate against the same pre-edit snapshot; overlapping ranges are rejected;
any failure applies zero writes. Success returns ±3 lines of fresh anchors (segmented if span >80 lines).
Never copy LINE:HASH→ prefixes into content. Never auto-shift stale anchors — use suggested anchors to retry.`
}

func (Edit) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "path":{"type":"string","description":"File path"},
  "edits":{
    "description":"Edit ops: array, single object, or double-JSON-encoded string (max 64)",
    "oneOf":[
      {"type":"array","items":{"type":"object"}},
      {"type":"object"},
      {"type":"string"}
    ]
  }
},
"required":["path","edits"]
}`)
}

func (Edit) ReadOnly() bool { return false }

// PermissionAlias documents the classic file-mutation family this maps to.
// permission.PermissionIdentity / Decide map hashline_edit → edit_file (and
// sole write → write_file) so deny=["edit_file"] covers anchor edits.
func (Edit) PermissionAlias() string { return "edit_file" }

// Preview computes the change hashline_edit would make without writing.
// Mirrors Execute's validation (anchors, overlap, sole-write) so approval cards
// and changed-files panels see the real target path and diff.
func (e Edit) Preview(args json.RawMessage) (diff.Change, error) {
	var raw struct {
		Path  string          `json:"path"`
		Edits json.RawMessage `json:"edits"`
	}
	if err := json.Unmarshal(args, &raw); err != nil {
		return diff.Change{}, fmt.Errorf("invalid args: %w", err)
	}
	if raw.Path == "" {
		return diff.Change{}, fmt.Errorf("path is required")
	}
	ops, err := ParseEdits(raw.Edits)
	if err != nil {
		return diff.Change{}, err
	}
	path := builtin.HashlineResolvePath(e.workDir, raw.Path)
	// Same write boundary as Execute (roots / session guard / managed config),
	// so approval cards never preview a path the call would refuse.
	if err := builtin.HashlineConfinePreview(e.roots, e.guard, e.managed, path); err != nil {
		return diff.Change{}, err
	}

	content, _, rerr := builtin.HashlineReadEncoded(path)
	if rerr != nil {
		if os.IsNotExist(rerr) && len(ops) == 1 && ops[0].Kind == "write" {
			return diff.Build(path, "", ops[0].Content, diff.Create), nil
		}
		return diff.Change{}, fmt.Errorf("read %s: %w", path, rerr)
	}
	result := ApplyEdits(content, ops)
	if result.Err != nil {
		return diff.Change{}, result.Err
	}
	return diff.Build(path, content, result.NewContent, diff.Modify), nil
}

var _ tool.Previewer = Edit{}

func (e Edit) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var raw struct {
		Path  string          `json:"path"`
		Edits json.RawMessage `json:"edits"`
	}
	if err := json.Unmarshal(args, &raw); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if raw.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	ops, err := ParseEdits(raw.Edits)
	if err != nil {
		return "", err
	}

	path := builtin.HashlineResolvePath(e.workDir, raw.Path)
	if err := builtin.HashlineConfineWrite(ctx, e.roots, e.guard, e.managed, path); err != nil {
		return "", err
	}

	// New-file write-only path.
	content, enc, rerr := builtin.HashlineReadEncoded(path)
	if rerr != nil {
		if os.IsNotExist(rerr) && len(ops) == 1 && ops[0].Kind == "write" {
			return e.writeNew(ctx, path, ops[0].Content)
		}
		return "", fmt.Errorf("read %s: %w", path, rerr)
	}

	result := ApplyEdits(content, ops)
	if result.Err != nil {
		return "", result.Err
	}

	if err := e.writeContent(ctx, path, result.NewContent, enc); err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "edited %s (%d op(s), scheme=%s)\n", path, result.Applied, SchemeName)
	if len(result.Warnings) > 0 {
		b.WriteString(strings.Join(result.Warnings, "\n"))
		b.WriteString("\n\n")
	}
	b.WriteString("Fresh anchors around the edit:\n")
	b.WriteString(result.Snippet)
	return b.String(), nil
}

func (e Edit) writeNew(ctx context.Context, path, content string) (string, error) {
	if line := detectAnchorPrefixInContent(content); line > 0 {
		return "", anchorContentError("write", content, line)
	}
	if err := e.writeContent(ctx, path, content, fileenc.UTF8); err != nil {
		return "", err
	}
	snippet := FormatHashlineContent(content, 0, snippetContext*2)
	return fmt.Sprintf("wrote %s (1 op, scheme=%s)\nFresh anchors:\n%s", path, SchemeName, snippet), nil
}

func (e Edit) writeContent(ctx context.Context, path, content string, enc fileenc.Kind) error {
	// Overlay for plain UTF-8 absolute paths (mirrors write_file).
	if e.overlay != nil && filepath.IsAbs(path) && (enc == fileenc.UTF8) {
		if ok, werr := e.overlay.WriteTextFile(ctx, path, content); ok {
			if werr != nil {
				return fmt.Errorf("write %s: %w", path, werr)
			}
			return nil
		}
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	if err := builtin.HashlineWriteEncoded(path, content, enc); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
