package hashline

import (
	"strings"
	"testing"
)

const sample = "fn main() {\n    let x = 1;\n    let y = 2;\n    println!(\"{x} {y}\");\n}\n"

func anchorsFor(content string) []string {
	lines := SplitLines(content)
	as := GenerateAnchors(lines)
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.Render()
	}
	return out
}

func TestApplyPointReplace(t *testing.T) {
	a := anchorsFor(sample)
	r := ApplyEdits(sample, []Op{{Kind: "replace", Anchor: a[1], Content: "    let x = 999;"}})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if !strings.Contains(r.NewContent, "999") || strings.Contains(r.NewContent, "let x = 1;") {
		t.Fatalf("content: %q", r.NewContent)
	}
	if !strings.Contains(r.Snippet, "\u2192") {
		t.Fatal("snippet missing anchors")
	}
	// Fresh anchors must re-validate.
	lines := SplitLines(r.NewContent)
	fresh := GenerateAnchors(lines)
	for _, fa := range fresh {
		if Validate(ParsedAnchor{Line: fa.Line, Local: fa.Local, Context: fa.Context}, lines) != Valid {
			t.Fatal("fresh anchors must validate")
		}
	}
}

func TestApplyDelete(t *testing.T) {
	a := anchorsFor(sample)
	r := ApplyEdits(sample, []Op{{Kind: "replace", Anchor: a[1], Content: ""}})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if strings.Contains(r.NewContent, "let x = 1;") {
		t.Fatal("delete failed")
	}
}

func TestApplyRangeReplace(t *testing.T) {
	a := anchorsFor(sample)
	r := ApplyEdits(sample, []Op{{Kind: "replace", Anchor: a[1], EndAnchor: a[2], Content: "    let z = 42;"}})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if !strings.Contains(r.NewContent, "42") {
		t.Fatal(r.NewContent)
	}
}

func TestApplyBOFAndEOF(t *testing.T) {
	r := ApplyEdits(sample, []Op{{Kind: "insert_after", Anchor: "0:", Content: "// header"}})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if !strings.HasPrefix(strings.TrimPrefix(r.NewContent, "\ufeff"), "// header\n") {
		t.Fatalf("bof: %q", r.NewContent)
	}

	r2 := ApplyEdits("line1\nline2\n", []Op{{Kind: "insert_after", Anchor: "EOF", Content: "line3"}})
	if r2.Err != nil {
		t.Fatal(r2.Err)
	}
	if !strings.Contains(r2.NewContent, "line2\nline3") {
		t.Fatalf("eof trailing nl: %q", r2.NewContent)
	}

	r3 := ApplyEdits("line1\nline2", []Op{{Kind: "insert_after", Anchor: "EOF", Content: "line3"}})
	if r3.Err != nil {
		t.Fatal(r3.Err)
	}
	if !strings.HasSuffix(r3.NewContent, "line3") {
		t.Fatalf("eof no nl: %q", r3.NewContent)
	}
}

func TestApplyWrite(t *testing.T) {
	r := ApplyEdits(sample, []Op{{Kind: "write", Content: "new\n"}})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if r.NewContent != "new\n" {
		t.Fatalf("%q", r.NewContent)
	}
}

func TestApplyWriteMustBeSole(t *testing.T) {
	a := anchorsFor(sample)
	r := ApplyEdits(sample, []Op{
		{Kind: "write", Content: "x"},
		{Kind: "replace", Anchor: a[0], Content: "y"},
	})
	if r.Err == nil || r.Err.Kind != ErrInvalidInput {
		t.Fatalf("want invalid, got %+v", r.Err)
	}
	if r.NewContent != "" {
		t.Fatal("partial write")
	}
}

func TestApplyStaleNoWrite(t *testing.T) {
	r := ApplyEdits(sample, []Op{{Kind: "replace", Anchor: "2:zzz:zzz", Content: "nope"}})
	if r.Err == nil || r.Err.Kind != ErrAnchorStale {
		t.Fatalf("got %+v", r.Err)
	}
	if r.NewContent != "" {
		t.Fatal("must not produce content on failure")
	}
	if r.Err.Context == "" || r.Err.Current == "" {
		t.Fatal("stale must include current+context")
	}
}

func TestApplyOverlapRejected(t *testing.T) {
	a := anchorsFor(sample)
	r := ApplyEdits(sample, []Op{
		{Kind: "replace", Anchor: a[1], EndAnchor: a[3], Content: "a"},
		{Kind: "replace", Anchor: a[2], Content: "b"},
	})
	if r.Err == nil || r.Err.Kind != ErrOverlappingEdits {
		t.Fatalf("got %+v", r.Err)
	}
	if r.NewContent != "" {
		t.Fatal("partial write on overlap")
	}
}

func TestApplyInsertInsideReplaceRejected(t *testing.T) {
	a := anchorsFor(sample)
	r := ApplyEdits(sample, []Op{
		{Kind: "replace", Anchor: a[1], EndAnchor: a[3], Content: "replaced"},
		{Kind: "insert_after", Anchor: a[2], Content: "ins"},
	})
	if r.Err == nil || r.Err.Kind != ErrOverlappingEdits {
		t.Fatalf("got %+v", r.Err)
	}
}

func TestApplyMisCopiedAnchorPrefix(t *testing.T) {
	a := anchorsFor(sample)
	r := ApplyEdits(sample, []Op{{
		Kind:    "replace",
		Anchor:  a[1],
		Content: a[1] + "\u2192    let x = 9;",
	}})
	if r.Err == nil || r.Err.Kind != ErrInvalidInput {
		t.Fatalf("got %+v", r.Err)
	}
	if r.NewContent != "" {
		t.Fatal("partial")
	}
}

func TestApplySameAnchorInsertOrder(t *testing.T) {
	a := anchorsFor(sample)
	r := ApplyEdits(sample, []Op{
		{Kind: "insert_after", Anchor: a[1], Content: "    // first"},
		{Kind: "insert_after", Anchor: a[1], Content: "    // second"},
	})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	fi := strings.Index(r.NewContent, "// first")
	se := strings.Index(r.NewContent, "// second")
	if fi < 0 || se < 0 || fi > se {
		t.Fatalf("order: %q", r.NewContent)
	}
}

func TestApplyBatchStaleRejectsAll(t *testing.T) {
	a := anchorsFor(sample)
	r := ApplyEdits(sample, []Op{
		{Kind: "replace", Anchor: a[0], Content: "valid"},
		{Kind: "replace", Anchor: "3:zzz:zzz", Content: "stale"},
	})
	if r.Err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(r.Err.Message, "none of the edits were applied") {
		t.Fatal(r.Err.Message)
	}
	if r.NewContent != "" {
		t.Fatal("partial batch write")
	}
}

func TestApplyMediumLargeWarnings(t *testing.T) {
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, "line")
	}
	content := strings.Join(lines, "\n") + "\n"
	a := anchorsFor(content)
	r := ApplyEdits(content, []Op{{Kind: "replace", Anchor: a[0], EndAnchor: a[9], Content: "x"}})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if len(r.Warnings) == 0 || !strings.Contains(r.Warnings[0], "medium") {
		t.Fatalf("warnings: %v", r.Warnings)
	}
	r2 := ApplyEdits(content, []Op{{Kind: "replace", Anchor: a[0], EndAnchor: a[24], Content: "x"}})
	if r2.Err != nil {
		t.Fatal(r2.Err)
	}
	if len(r2.Warnings) == 0 || !strings.Contains(r2.Warnings[0], "large") {
		t.Fatalf("warnings: %v", r2.Warnings)
	}
}

func TestApplyCRLFPreserved(t *testing.T) {
	content := "a\r\nb\r\nc\r\n"
	a := anchorsFor(content)
	r := ApplyEdits(content, []Op{{Kind: "replace", Anchor: a[1], Content: "B"}})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if !strings.Contains(r.NewContent, "\r\n") {
		t.Fatalf("lost CRLF: %q", r.NewContent)
	}
	if strings.Contains(strings.ReplaceAll(r.NewContent, "\r\n", ""), "\n") {
		t.Fatalf("bare LF leaked: %q", r.NewContent)
	}
}

func TestApplyMaxEdits(t *testing.T) {
	ops := make([]Op, MaxEdits+1)
	for i := range ops {
		ops[i] = Op{Kind: "insert_after", Anchor: "EOF", Content: "x"}
	}
	r := ApplyEdits("a\n", ops)
	if r.Err == nil || r.Err.Kind != ErrInvalidInput {
		t.Fatalf("got %+v", r.Err)
	}
}

func TestApplyPropertyNoPartialWrite(t *testing.T) {
	// Property-style: any failing batch leaves NewContent empty.
	a := anchorsFor(sample)
	bad := [][]Op{
		{{Kind: "replace", Anchor: "bad", Content: "x"}},
		{{Kind: "replace", Anchor: a[0], Content: a[0] + "\u2192x"}},
		{{Kind: "replace", Anchor: a[1], EndAnchor: a[0], Content: "x"}},
		{
			{Kind: "replace", Anchor: a[1], EndAnchor: a[3], Content: "a"},
			{Kind: "replace", Anchor: a[2], Content: "b"},
		},
	}
	for i, ops := range bad {
		r := ApplyEdits(sample, ops)
		if r.Err == nil {
			t.Fatalf("case %d: expected error", i)
		}
		if r.NewContent != "" {
			t.Fatalf("case %d: partial write %q", i, r.NewContent)
		}
	}
}

func TestApplySuccessRegeneratesVerifiableAnchors(t *testing.T) {
	a := anchorsFor(sample)
	r := ApplyEdits(sample, []Op{
		{Kind: "insert_after", Anchor: a[0], Content: "    // note"},
		{Kind: "replace", Anchor: a[3], Content: "    println!(\"hi\");"},
	})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	lines := SplitLines(r.NewContent)
	for _, fa := range GenerateAnchors(lines) {
		if Validate(ParsedAnchor{Line: fa.Line, Local: fa.Local, Context: fa.Context}, lines) != Valid {
			t.Fatal("unverifiable anchor after success")
		}
	}
	// Snippet anchors should parse and match generated.
	for _, line := range strings.Split(r.Snippet, "\n") {
		if strings.HasPrefix(line, "...") || line == "" {
			continue
		}
		before, _, ok := strings.Cut(line, "\u2192")
		if !ok {
			continue
		}
		p, ok := ParseAnchor(before)
		if !ok {
			t.Fatalf("snippet anchor %q", before)
		}
		if Validate(p, lines) != Valid {
			t.Fatalf("snippet anchor stale: %s", before)
		}
	}
}

func TestParseEditsDoubleJSON(t *testing.T) {
	ops, err := ParseEdits([]byte(`[{"op":"replace","anchor":"1:ab:cd","content":"x"}]`))
	if err != nil || len(ops) != 1 {
		t.Fatalf("%v %v", ops, err)
	}
	ops, err = ParseEdits([]byte(`"[{\"op\":\"replace\",\"anchor\":\"1:ab:cd\",\"content\":\"x\"}]"`))
	if err != nil || len(ops) != 1 {
		t.Fatalf("double json: %v %v", ops, err)
	}
	ops, err = ParseEdits([]byte(`{"op":"insert_after","anchor":"EOF","content":"z"}`))
	if err != nil || len(ops) != 1 || ops[0].Kind != "insert_after" {
		t.Fatalf("object: %v %v", ops, err)
	}
}

func TestRangePolicy(t *testing.T) {
	if RangeWarning(0, 3) != "" {
		t.Fatal("small")
	}
	if !strings.Contains(RangeWarning(0, 10), "medium") {
		t.Fatal("medium")
	}
	if !strings.Contains(RangeWarning(0, 30), "large") {
		t.Fatal("large")
	}
}

func TestApplyScatteredSnippet(t *testing.T) {
	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, "line_"+itoa(i))
	}
	content := strings.Join(lines, "\n") + "\n"
	a := anchorsFor(content)
	r := ApplyEdits(content, []Op{
		{Kind: "replace", Anchor: a[4], Content: "TOP"},
		{Kind: "replace", Anchor: a[194], Content: "BOTTOM"},
	})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if !strings.Contains(r.Snippet, "TOP") || !strings.Contains(r.Snippet, "BOTTOM") {
		t.Fatal(r.Snippet)
	}
	if !strings.Contains(r.Snippet, "lines not shown") {
		t.Fatal("expected gap markers")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}
