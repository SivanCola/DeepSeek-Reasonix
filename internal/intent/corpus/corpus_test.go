package corpus

import "testing"

// The corpus is consumed by t.Run subtests in internal/agent and by the live
// classifier evaluation. These invariants catch the mistakes that would silently
// weaken either consumer: a duplicate name makes two subtests collide, a
// duplicate text double-counts accuracy, and a case with no labels asserts
// nothing while still looking like coverage.
func TestCorpusInvariants(t *testing.T) {
	sets := map[string][]Case{
		"Delivery":   Delivery,
		"Task":       Task,
		"GoalBudget": GoalBudget,
	}

	for name, set := range sets {
		t.Run(name, func(t *testing.T) {
			if len(set) == 0 {
				t.Fatal("corpus set is empty")
			}
			names := map[string]bool{}
			texts := map[string]bool{}
			for i, c := range set {
				if c.Name == "" {
					t.Errorf("case %d has no name", i)
				}
				if names[c.Name] {
					t.Errorf("duplicate case name %q: subtests would collide", c.Name)
				}
				names[c.Name] = true

				// Empty text is a deliberate edge case; only guard duplicates.
				if c.Text != "" {
					if texts[c.Text] {
						t.Errorf("duplicate case text %q", c.Text)
					}
					texts[c.Text] = true
				}

				if c.NeedsEvidence == nil && c.NeedsMutation == nil &&
					c.IsTask == nil && c.NeedsWriteBudget == nil {
					t.Errorf("case %q carries no labels and asserts nothing", c.Name)
				}
			}
		})
	}
}

// TestAllCarriesOrigin pins that All stamps provenance, which the evaluation
// harness reports so a disagreement can be traced back to the test that owns it.
func TestAllCarriesOrigin(t *testing.T) {
	all := All()
	if want := len(Delivery) + len(Task) + len(GoalBudget); len(all) != want {
		t.Fatalf("All() returned %d cases, want %d", len(all), want)
	}
	for _, c := range all {
		if c.Origin == "" {
			t.Errorf("case %q has no Origin", c.Name)
		}
	}
}
