package hashline

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"golang.org/x/text/encoding/simplifiedchinese"

	"reasonix/internal/sandbox"
	"reasonix/internal/tool"
	"reasonix/internal/tool/builtin"
)

func jsonArgs(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHashlineReadBasic(t *testing.T) {
	dir := t.TempDir()
	body := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rd := NewRead(dir, nil, nil, nil)
	out, err := rd.Execute(context.Background(), jsonArgs(t, map[string]any{"path": "main.go"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\u2192package main") {
		t.Fatalf("got %q", out)
	}
	first := strings.Split(out, "\n")[0]
	before, _, ok := strings.Cut(first, "\u2192")
	if !ok || strings.Count(before, ":") != 2 {
		t.Fatalf("anchor form: %q", first)
	}
}

func TestHashlineReadEmptyAndCJK(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "empty.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	rd := NewRead(dir, nil, nil, nil)
	out, err := rd.Execute(context.Background(), jsonArgs(t, map[string]any{"path": "empty.txt"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "empty file") || !strings.Contains(out, "\u2192") {
		t.Fatalf("%q", out)
	}

	cjk := "第一行\n第二行\n"
	if err := os.WriteFile(filepath.Join(dir, "cjk.txt"), []byte(cjk), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = rd.Execute(context.Background(), jsonArgs(t, map[string]any{"path": "cjk.txt"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "第一行") {
		t.Fatal(out)
	}
}

func TestHashlineReadPagination(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < 10; i++ {
		b.WriteString("line\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "p.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	rd := NewRead(dir, nil, nil, nil)
	out, err := rd.Execute(context.Background(), jsonArgs(t, map[string]any{
		"path": "p.txt", "offset": 2, "limit": 2,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "3:") {
		t.Fatalf("%q", out)
	}
	if !strings.Contains(out, "offset=4") {
		t.Fatalf("pagination trailer: %q", out)
	}
}

func TestHashlineEditAndReadEncoding(t *testing.T) {
	dir := t.TempDir()
	src := "第一行 hello\r\n第二行 world\r\n"
	enc, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "f.cs")
	if err := os.WriteFile(path, enc, 0o644); err != nil {
		t.Fatal(err)
	}
	content, _, err := builtin.HashlineReadEncoded(path)
	if err != nil {
		t.Fatal(err)
	}
	a := anchorsFor(content)
	ed := NewEdit(dir, []string{dir}, builtin.SessionDataGuard{}, builtin.ManagedConfigPaths{}, nil)
	_, err = ed.Execute(context.Background(), jsonArgs(t, map[string]any{
		"path": "f.cs",
		"edits": []map[string]any{
			{"op": "replace", "anchor": a[0], "content": "第一行 HELLO"},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := simplifiedchinese.GB18030.NewDecoder().Bytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	got := string(dec)
	if !strings.Contains(got, "HELLO") || !strings.Contains(got, "\r\n") {
		t.Fatalf("encoding/endings: %q", got)
	}
}

func TestHashlineEditUTF16BOM(t *testing.T) {
	dir := t.TempDir()
	text := "alpha\nbeta\n"
	u := utf16.Encode([]rune(text))
	buf := make([]byte, 2+len(u)*2)
	buf[0], buf[1] = 0xFF, 0xFE
	for i, v := range u {
		binary.LittleEndian.PutUint16(buf[2+i*2:], v)
	}
	path := filepath.Join(dir, "u.txt")
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	content, _, err := builtin.HashlineReadEncoded(path)
	if err != nil {
		t.Fatal(err)
	}
	a := anchorsFor(content)
	ed := NewEdit(dir, []string{dir}, builtin.SessionDataGuard{}, builtin.ManagedConfigPaths{}, nil)
	_, err = ed.Execute(context.Background(), jsonArgs(t, map[string]any{
		"path":  "u.txt",
		"edits": []map[string]any{{"op": "replace", "anchor": a[0], "content": "ALPHA"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if len(raw) < 2 || raw[0] != 0xFF || raw[1] != 0xFE {
		t.Fatalf("BOM lost: %v", raw[:min(4, len(raw))])
	}
}

func TestHashlineEditDoubleJSONAndStale(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "t.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := anchorsFor("a\nb\n")
	ed := NewEdit(dir, []string{dir}, builtin.SessionDataGuard{}, builtin.ManagedConfigPaths{}, nil)

	inner, _ := json.Marshal([]map[string]any{
		{"op": "replace", "anchor": a[0], "content": "A"},
	})
	_, err := ed.Execute(context.Background(), jsonArgs(t, map[string]any{
		"path":  "t.txt",
		"edits": string(inner),
	}))
	if err != nil {
		t.Fatal(err)
	}

	before, _ := os.ReadFile(filepath.Join(dir, "t.txt"))
	_, err = ed.Execute(context.Background(), jsonArgs(t, map[string]any{
		"path":  "t.txt",
		"edits": []map[string]any{{"op": "replace", "anchor": "1:zzz:zzz", "content": "X"}},
	}))
	if err == nil {
		t.Fatal("expected stale error")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "t.txt"))
	if string(before) != string(after) {
		t.Fatalf("stale wrote to disk: %q → %q", before, after)
	}
}

func TestHashlineGrepAnchors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "s.go"), []byte("package main\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := NewGrep(dir, nil, builtin.SearchSpec{}, sandbox.Spec{}, nil)
	out, err := g.Execute(context.Background(), jsonArgs(t, map[string]any{
		"pattern": "Hello",
		"path":    ".",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if out == "(no matches)" {
		t.Fatal("expected match")
	}
	found := false
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Hello") && strings.Count(line, ":") >= 4 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected anchored line, got %q", out)
	}
}

func TestNotInBuiltinRegistry(t *testing.T) {
	// Ensure blank-import of this package wouldn't be required; constructors only.
	// Classic Builtins must not include hashline tools.
	for _, name := range ToolNames {
		if _, ok := tool.LookupBuiltin(name); ok {
			t.Fatalf("%s must not auto-register into Builtins", name)
		}
	}
	// Config.Tools still constructs them for hashline sessions.
	tools := Config{WorkDir: t.TempDir()}.Tools()
	if len(tools) != 3 {
		t.Fatalf("Tools() = %d", len(tools))
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Name()] = true
		if tl.Name() == "hashline_read" && !tl.ReadOnly() {
			t.Fatal("read must be ReadOnly")
		}
		if tl.Name() == "hashline_edit" && tl.ReadOnly() {
			t.Fatal("edit must not be ReadOnly")
		}
	}
	for _, n := range ToolNames {
		if !names[n] {
			t.Fatalf("missing %s", n)
		}
	}
}

func TestPermissionAliasesDocumented(t *testing.T) {
	if (Read{}).PermissionAlias() != "read_file" {
		t.Fatal()
	}
	if (Edit{}).PermissionAlias() != "edit_file" {
		t.Fatal()
	}
	if (Grep{}).PermissionAlias() != "grep" {
		t.Fatal()
	}
}

func TestFuzzFailureNeverPartialWriteOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	orig := "one\ntwo\nthree\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	ed := NewEdit(dir, []string{dir}, builtin.SessionDataGuard{}, builtin.ManagedConfigPaths{}, nil)
	a := anchorsFor(orig)
	// Mix of failing batches
	batches := []any{
		[]map[string]any{{"op": "replace", "anchor": "nope", "content": "x"}},
		[]map[string]any{{"op": "replace", "anchor": a[0], "content": a[0] + "\u2192x"}},
		[]map[string]any{
			{"op": "replace", "anchor": a[0], "end_anchor": a[2], "content": "a"},
			{"op": "replace", "anchor": a[1], "content": "b"},
		},
	}
	for i, batch := range batches {
		_, err := ed.Execute(context.Background(), jsonArgs(t, map[string]any{"path": "x.txt", "edits": batch}))
		if err == nil {
			t.Fatalf("batch %d expected error", i)
		}
		got, _ := os.ReadFile(path)
		if string(got) != orig {
			t.Fatalf("batch %d partial write: %q", i, got)
		}
	}
}
