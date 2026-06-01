package cli

import (
	"strings"
	"testing"
)

func TestRenderBranchTreeStylesVisualWeight(t *testing.T) {
	oldColor := colorEnabled
	colorEnabled = true
	defer func() { colorEnabled = oldColor }()

	got := renderBranchTree("branches:\n├─ 0601-030143.318  你是谁  3 turns\n│  └─ 0601-033937.165  JSON response: success  1 turn\n└─ 0601-035153.346  JSON array  1 turn  current")
	for _, want := range []string{
		ansiAccent + "branches:" + ansiReset,
		ansiDim + "├─ " + ansiReset,
		ansiDim + "0601-030143.318" + ansiReset,
		ansiDim + "│  └─ " + ansiReset,
		ansiDim + "0601-033937.165" + ansiReset,
		ansiDim + "3 turns" + ansiReset,
		ansiAccent + "current" + ansiReset,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("styled tree missing %q:\n%q", want, got)
		}
	}
	if strings.Contains(got, "*") {
		t.Fatalf("styled tree should not use a duplicate current marker:\n%q", got)
	}
}
