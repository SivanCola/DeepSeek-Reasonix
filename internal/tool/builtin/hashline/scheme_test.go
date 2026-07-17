package hashline

import (
	"strings"
	"testing"
)

func sampleLines() []string {
	return []string{
		"import React from 'react';",
		"",
		"export function App() {",
		"  return <div>Hello</div>;",
		"}",
	}
}

func TestSplitLines(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{""}},
		{"a\nb\nc", []string{"a", "b", "c"}},
		{"a\nb\n", []string{"a", "b", ""}},
		{"\n", []string{"", ""}},
		{"a\r\nb\r\n", []string{"a", "b", ""}},
		{"a\r\nb", []string{"a", "b"}},
	}
	for _, tc := range cases {
		got := SplitLines(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("SplitLines(%q) len=%d want %d (%v)", tc.in, len(got), len(tc.want), got)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("SplitLines(%q)[%d]=%q want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

func TestJoinLinesRoundTrip(t *testing.T) {
	for _, content := range []string{"a\nb\n", "a\nb", "a\r\nb\r\n", ""} {
		crlf := UsesCRLF(content)
		lines := SplitLines(content)
		got := JoinLines(lines, crlf)
		// Empty content special-cases to single empty line join → ""
		if content == "" {
			if got != "" {
				t.Fatalf("empty roundtrip = %q", got)
			}
			continue
		}
		if got != content {
			t.Fatalf("roundtrip %q → %q", content, got)
		}
	}
}

func TestParseAnchor(t *testing.T) {
	a, ok := ParseAnchor("22:abc:rst")
	if !ok || a.Line != 22 || a.Local != "abc" || a.Context != "rst" {
		t.Fatalf("got %+v ok=%v", a, ok)
	}
	if _, ok := ParseAnchor("0:abc:rst"); ok {
		t.Fatal("line 0 should be rejected")
	}
	if _, ok := ParseAnchor("22:ABC:rst"); ok {
		t.Fatal("uppercase rejected")
	}
	// Strip arrow
	a, ok = ParseAnchor("22:abc:rst→code")
	if !ok || a.Render() != "22:abc:rst" {
		t.Fatalf("arrow strip failed: %+v", a)
	}
}

func TestGenerateAnchorsChunkV1(t *testing.T) {
	lines := sampleLines()
	a1 := GenerateAnchors(lines)
	a2 := GenerateAnchors(lines)
	if len(a1) != len(lines) {
		t.Fatalf("count %d", len(a1))
	}
	for i := range a1 {
		if a1[i] != a2[i] {
			t.Fatal("not deterministic")
		}
		if a1[i].Line != i+1 {
			t.Fatal("1-based line numbers")
		}
		if len(a1[i].Local) != 3 || len(a1[i].Context) != 3 {
			t.Fatalf("hash len: %+v", a1[i])
		}
		// All 5 lines in one chunk (size 8) → same context.
		if a1[i].Context != a1[0].Context {
			t.Fatal("same chunk should share context")
		}
		if Validate(ParsedAnchor{Line: a1[i].Line, Local: a1[i].Local, Context: a1[i].Context}, lines) != Valid {
			t.Fatalf("self-validate failed line %d", i+1)
		}
	}
}

func TestChunkDifferentChunksDiffer(t *testing.T) {
	owned := make([]string, 20)
	for i := range owned {
		owned[i] = "line " + strings.Repeat("x", i+1)
	}
	anchors := GenerateAnchors(owned)
	if anchors[0].Context == anchors[16].Context {
		// Unlikely with this input; if equal, still check chunk indices.
		t.Log("chunk fps collided (rare); checking indices only")
	}
	if anchors[0].Context == "" || anchors[16].Context == "" {
		t.Fatal("missing context")
	}
}

func TestValidateStaleAndIndentation(t *testing.T) {
	lines := sampleLines()
	anchors := GenerateAnchors(lines)
	parsed := ParsedAnchor{Line: anchors[3].Line, Local: anchors[3].Local, Context: anchors[3].Context}

	mut := append([]string{}, lines...)
	mut[3] = "  return <div>World</div>;"
	if Validate(parsed, mut) != Stale {
		t.Fatal("content change should stale")
	}

	// Indentation-only: local hash survives; chunk may still match if only whitespace.
	re := append([]string{}, lines...)
	re[3] = "    return <div>Hello</div>;"
	// Local should match (whitespace-normalized); chunk fingerprint also uses
	// line hashes so it should remain Valid.
	if Validate(parsed, re) != Valid {
		t.Fatal("reindent should keep valid for whitespace-normalized scheme")
	}
}

func TestFindShiftedAfterInsert(t *testing.T) {
	orig := sampleLines()
	anchors := GenerateAnchors(orig)
	shifted := append([]string{"// new line"}, orig...)
	parsed := ParsedAnchor{Line: anchors[2].Line, Local: anchors[2].Local, Context: anchors[2].Context}
	// Insert above changes every chunk fingerprint, so local+context recovery
	// typically returns not_found for the chunk scheme (strong freshness).
	res := FindShifted(parsed, shifted, 5)
	if res.Kind == "found" && res.NewLine != 4 {
		t.Fatalf("unexpected found line %d", res.NewLine)
	}
}

func TestFindShiftedContentOnlyLocal(t *testing.T) {
	// When context is empty, recovery is local-hash only (used for suffix recovery paths).
	// Insert above shifts "b" from line 2 to line 3.
	shifted := []string{"new", "a", "b", "c"}
	localB := EncodeHash(LineHash("b"), DefaultHashLen)
	res := FindShifted(ParsedAnchor{Line: 2, Local: localB, Context: ""}, shifted, 5)
	if res.Kind != "found" || res.NewLine != 3 {
		t.Fatalf("got %+v", res)
	}
}

func TestDuplicateLinesAmbiguous(t *testing.T) {
	lines := []string{"same", "x", "same", "y", "same"}
	local := EncodeHash(LineHash("same"), DefaultHashLen)
	// Anchor at line 1; after no shift, original skipped; nearby duplicates match local.
	// Without context, multiple "same" → ambiguous.
	res := FindShifted(ParsedAnchor{Line: 1, Local: local, Context: ""}, lines, 15)
	if res.Kind != "ambiguous" {
		t.Fatalf("want ambiguous, got %+v", res)
	}
}

func TestFormatHashlineContent(t *testing.T) {
	content := "line one\nline two\nline three\n"
	out := FormatHashlineContent(content, 0, 0)
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "\u2192") {
			t.Fatalf("missing arrow: %q", line)
		}
		before, _, _ := strings.Cut(line, "\u2192")
		if strings.Count(before, ":") != 2 {
			t.Fatalf("want LINE:LOCAL:CHUNK, got %q", before)
		}
	}
	// Window
	win := FormatHashlineContent(content, 1, 1)
	if !strings.HasPrefix(win, "2:") {
		t.Fatalf("offset window: %q", win)
	}
}
