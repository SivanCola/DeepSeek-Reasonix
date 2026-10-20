package serve

import (
	"strings"
	"testing"
)

// The packaged page sits in the app's resources folder, which on macOS is
// inside the bundle; a hint that stops at "resources" points one level short.
func TestPageMissingNamesWhereTheMacBundleKeepsThePage(t *testing.T) {
	if n := strings.Count(pageMissing, "Contents/Resources/frontend-next/dist"); n != 2 {
		t.Fatalf("the macOS path appears %d times, want once per language", n)
	}
}
