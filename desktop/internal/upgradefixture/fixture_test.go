package upgradefixture

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunRestoresEnvironmentOnSuccessAndFailure(t *testing.T) {
	for _, key := range []string{"REASONIX_HOME", "REASONIX_STATE_HOME", "REASONIX_CACHE_HOME"} {
		t.Setenv(key, "untouched-"+key)
	}
	home := filepath.Join(t.TempDir(), "isolated")
	report := filepath.Join(t.TempDir(), "fixture.json")
	for _, mode := range []string{"create", "verify", "invalid"} {
		err := Run(mode, home, report, "first")
		if mode == "create" && err != nil {
			t.Fatal(err)
		}
		if mode != "create" && err == nil {
			t.Fatalf("%s must fail before migration", mode)
		}
		for _, key := range []string{"REASONIX_HOME", "REASONIX_STATE_HOME", "REASONIX_CACHE_HOME"} {
			if got := os.Getenv(key); got != "untouched-"+key {
				t.Fatalf("%s leaked %s=%q", mode, key, got)
			}
		}
	}
}
