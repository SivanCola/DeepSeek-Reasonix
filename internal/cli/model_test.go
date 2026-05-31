package cli

import (
	"strings"
	"testing"
)

// TestModelRefsFromConfig verifies the /model picker enumerates configured
// provider/model refs (built-in defaults when no reasonix.toml is present).
func TestModelRefsFromConfig(t *testing.T) {
	t.Chdir(t.TempDir()) // no reasonix.toml → built-in default providers
	refs := modelRefs()
	if len(refs) == 0 {
		t.Fatal("expected default provider/model refs, got none")
	}
	for _, r := range refs {
		if !strings.Contains(r, "/") {
			t.Errorf("ref %q should be provider/model", r)
		}
	}
}

// TestModelArgCompletion verifies "/model " completes to the configured refs
// through the shared completion path.
func TestModelArgCompletion(t *testing.T) {
	t.Chdir(t.TempDir())
	m := newTestChatTUI()
	items, _, ok := m.slashArgItems("/model ")
	if !ok || len(items) == 0 {
		t.Fatalf("/model arg completion should offer refs, ok=%v n=%d", ok, len(items))
	}
}
