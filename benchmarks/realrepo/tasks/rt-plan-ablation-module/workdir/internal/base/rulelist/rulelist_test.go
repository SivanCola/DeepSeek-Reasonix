package rulelist

import (
	"errors"
	"reflect"
	"testing"
)

func TestSplitSeparatesOnCommasAndWhitespaceOutsideParentheses(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"Read", []string{"Read"}},
		{"read_file, grep,bash", []string{"read_file", "grep", "bash"}},
		{"Read Grep  Bash", []string{"Read", "Grep", "Bash"}},
		{"Bash(git add:*) Read", []string{"Bash(git add:*)", "Read"}},
		{"Bash(git add:*, git commit:*), Read", []string{"Bash(git add:*, git commit:*)", "Read"}},
		{"bash,", []string{"bash"}},
	} {
		got, err := Split(tc.in)
		if err != nil {
			t.Fatalf("Split(%q): %v", tc.in, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Split(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSplitReportsUnbalancedParenthesesByIdentity(t *testing.T) {
	if _, err := Split("Bash(git add"); !errors.Is(err, ErrUnclosed) {
		t.Errorf("unclosed = %v", err)
	}
	if _, err := Split("Bash)"); !errors.Is(err, ErrUnexpectedClose) {
		t.Errorf("unexpected close = %v", err)
	}
}
