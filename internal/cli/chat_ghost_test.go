package cli

import (
	"strings"
	"testing"
)

// TestClampWidth guards the inline-overflow fix: scrollback lines wider than the
// viewport get hard-broken (so the renderer's scroll estimate stays exact), while
// lines within width — including space-padded table rows — are left untouched.
func TestClampWidth(t *testing.T) {
	// Within width: byte-for-byte identical (runs of spaces must NOT collapse).
	row := "│ a    │ bb │"
	if got := clampWidth(row, 80); got != row {
		t.Errorf("within-width line altered: %q -> %q", row, got)
	}
	// Over width: every resulting line fits, content is preserved.
	long := strings.Repeat("x", 200)
	out := clampWidth(long, 40)
	for _, line := range strings.Split(out, "\n") {
		if visibleWidth(line) > 40 {
			t.Errorf("clamped line exceeds 40: width=%d", visibleWidth(line))
		}
	}
	if strings.ReplaceAll(out, "\n", "") != long {
		t.Error("clampWidth lost or altered content")
	}
	// width <= 0 is a no-op (pre-sizing).
	if clampWidth(long, 0) != long {
		t.Error("width<=0 should be a no-op")
	}
}

// TestCommitReasoningDropsHiddenBuffer guards the default TUI contract:
// reasoning must not be queued into user-visible scrollback.
func TestCommitReasoningDropsHiddenBuffer(t *testing.T) {
	const width = 40
	commit := []string{}
	m := &chatTUI{
		width:         width,
		reasoning:     &strings.Builder{},
		pendingCommit: &commit,
	}
	m.reasoning.WriteString("\x1b[2m  ▎ thinking\x1b[0m\n")
	m.reasoning.WriteString("\x1b[2m" + strings.Repeat("reason ", 30) + "\x1b[0m")

	m.commitReasoning()

	if len(commit) != 0 {
		t.Fatalf("commitReasoning should not queue visible output, got %q", commit)
	}
	if m.reasoning.Len() != 0 {
		t.Error("reasoning buffer not reset")
	}
}

// TestChunkLines guards the overflow fix: long blocks split into screen-bounded
// pieces (order + content preserved); short blocks pass through whole.
func TestChunkLines(t *testing.T) {
	if got := chunkLines("a\nb\nc", 5); len(got) != 1 || got[0] != "a\nb\nc" {
		t.Errorf("short block should pass whole: %q", got)
	}
	got := chunkLines("1\n2\n3\n4\n5", 2)
	if len(got) != 3 || got[0] != "1\n2" || got[1] != "3\n4" || got[2] != "5" {
		t.Errorf("chunking wrong: %q", got)
	}
	// Rejoining the chunks reproduces the original.
	if strings.Join(got, "\n") != "1\n2\n3\n4\n5" {
		t.Error("chunkLines lost content/order")
	}
}
