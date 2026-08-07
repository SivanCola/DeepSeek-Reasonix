package taskintent

import (
	"testing"

	"reasonix/internal/intent/corpus"
)

// TestHeuristicInputIsTask covers the delivery evidence gate's task-vs-chat
// heuristic: greetings and acknowledgements must not arm the delivery gates,
// while actionable requests and failure descriptions must.
//
// The case table lives in internal/intent/corpus so this gate test and the
// classifier evaluation harness judge one corpus.
func TestHeuristicInputIsTask(t *testing.T) {
	for _, tt := range corpus.Task {
		if tt.IsTask == nil {
			continue
		}
		t.Run(tt.Name, func(t *testing.T) {
			if got := heuristicInputIsTask(tt.Text); got != *tt.IsTask {
				t.Errorf("heuristicInputIsTask(%q) = %v, want %v", tt.Text, got, *tt.IsTask)
			}
		})
	}
}
