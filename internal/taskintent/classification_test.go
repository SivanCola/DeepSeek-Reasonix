package taskintent

import (
	"testing"

	"reasonix/internal/intent/corpus"
)

// TestDeliveryClassificationMatrix protects the boundary between advisory
// troubleshooting, observable read-only work, and mutation delivery.
//
// The case table lives in internal/intent/corpus so this gate test and the
// classifier evaluation harness judge one corpus. Holding a private copy here
// is what let the keyword tables drift apart in the first place.
func TestDeliveryClassificationMatrix(t *testing.T) {
	for _, tt := range corpus.Delivery {
		t.Run(tt.Name, func(t *testing.T) {
			if tt.NeedsEvidence != nil {
				if got := NeedsEvidence(tt.Text); got != *tt.NeedsEvidence {
					t.Errorf("NeedsEvidence(%q) = %v, want %v", tt.Text, got, *tt.NeedsEvidence)
				}
			}
			if tt.NeedsMutation != nil {
				if got := NeedsMutation(tt.Text); got != *tt.NeedsMutation {
					t.Errorf("NeedsMutation(%q) = %v, want %v", tt.Text, got, *tt.NeedsMutation)
				}
			}
		})
	}
}
