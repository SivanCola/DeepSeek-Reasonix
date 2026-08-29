package agent

import (
	"strings"
	"testing"
)

func TestDedupeProviderVisibleResultOmitsExactRepeats(t *testing.T) {
	a := &Agent{}
	first := a.dedupeProviderVisibleResult("c1", "hello world", "hello world")
	if first != "hello world" {
		t.Fatalf("first = %q", first)
	}
	second := a.dedupeProviderVisibleResult("c2", "hello world", "hello world")
	if !strings.Contains(second, "duplicate tool result") || !strings.Contains(second, "c1") {
		t.Fatalf("second = %q", second)
	}
}

func TestDedupeUsesRawResultBeforeLossySummary(t *testing.T) {
	a := &Agent{}
	visible := "same bounded failure summary"
	first := a.dedupeProviderVisibleResult("c1", "prefix hidden-one suffix", visible)
	second := a.dedupeProviderVisibleResult("c2", "prefix hidden-two suffix", visible)
	if first != visible || second != visible {
		t.Fatalf("distinct raw results were deduped: first=%q second=%q", first, second)
	}
}

func TestSummarizeCIOutputKeepsFailures(t *testing.T) {
	body := "##teamcity[testFailed name='A']\n" + strings.Repeat("ok\n", 400) + "exit status 1\n"
	got := summarizeCIOutput(body)
	if !strings.Contains(got, "exit_code: 1") || !strings.Contains(got, "testFailed") {
		t.Fatalf("summary = %q", got)
	}
	if len(got) >= len(body) {
		t.Fatal("summary should be smaller than the raw CI log")
	}
}
