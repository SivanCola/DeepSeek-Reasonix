package hashline

import "testing"

func TestFNV1a32Empty(t *testing.T) {
	if got := FNV1a32(nil); got != fnvOffset {
		t.Fatalf("empty = %d, want %d", got, fnvOffset)
	}
	if got := FNV1a32([]byte{}); got != fnvOffset {
		t.Fatalf("empty slice = %d, want %d", got, fnvOffset)
	}
}

func TestFNV1a32Deterministic(t *testing.T) {
	a := FNV1a32([]byte("hello world"))
	b := FNV1a32([]byte("hello world"))
	if a != b {
		t.Fatal("not deterministic")
	}
	if a == FNV1a32([]byte("world")) {
		t.Fatal("different inputs should differ")
	}
}

func TestLineHashWhitespaceNormalization(t *testing.T) {
	a := LineHash("    let x = 1;")
	b := LineHash("  let x = 1;")
	c := LineHash("\tlet x = 1;")
	d := LineHash("let x = 1;   ")
	e := LineHash("let  x  =  1;")
	if a != b || b != c || c != d || d != e {
		t.Fatalf("whitespace variants differ: %d %d %d %d %d", a, b, c, d, e)
	}
}

func TestLineHashPreservesTokenBoundaries(t *testing.T) {
	if LineHash("return x") == LineHash("returnx") {
		t.Fatal("token boundary must change hash")
	}
}

func TestLineHashEmpty(t *testing.T) {
	if LineHash("") != LineHash("   ") || LineHash("") != LineHash("\t\t") {
		t.Fatal("empty/whitespace-only should match")
	}
}

func TestLineHashCJK(t *testing.T) {
	a := LineHash("  你好世界  ")
	b := LineHash("你好世界")
	if a != b {
		t.Fatal("CJK whitespace trim failed")
	}
	if LineHash("你好") == LineHash("世界") {
		t.Fatal("different CJK must differ")
	}
}

func TestEncodeHash(t *testing.T) {
	h := FNV1a32([]byte("test"))
	enc := EncodeHash(h, 3)
	if len(enc) != 3 {
		t.Fatalf("len=%d", len(enc))
	}
	for _, c := range enc {
		if c < 'a' || c > 'z' {
			t.Fatalf("non-lowercase: %q", enc)
		}
	}
	if EncodeHash(h, 3) != enc {
		t.Fatal("not deterministic")
	}
	// Known values cross-checked against Python port of the algorithm.
	if got := EncodeHash(LineHash("let x = 1;"), 3); got != "rsj" {
		t.Fatalf("let x = 1; hash encode = %q, want rsj", got)
	}
	if got := EncodeHash(LineHash(""), 3); got != "ddg" {
		t.Fatalf("empty line encode = %q, want ddg", got)
	}
}

func TestLineHashKnownValues(t *testing.T) {
	// Cross-check against the Rust/Python FNV-1a port used in grok-build.
	if got := LineHash("hello world"); got != 3582672807 {
		t.Fatalf("hello world = %d, want 3582672807", got)
	}
}
