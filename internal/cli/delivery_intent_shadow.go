package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"reasonix/internal/agent"
)

// deliveryIntentShadowEnv opts a session into printing the delivery intent
// shadow counters when it ends.
const deliveryIntentShadowEnv = "REASONIX_INTENT_SHADOW"

// reportDeliveryIntentShadow prints how often each tier of the delivery intent
// resolver answered, and how often it agreed with the keyword classifier that
// still drives behavior.
//
// It exists to answer one migration question with real traffic rather than
// synthetic tests: what share of turns the two zero-cost tiers resolve on their
// own. That share decides whether a per-turn model classifier is affordable.
//
// Opt-in and stderr-only. It writes no persisted format and prints nothing for
// ordinary sessions, so it can ship while the migration is measured.
func reportDeliveryIntentShadow() {
	if strings.TrimSpace(os.Getenv(deliveryIntentShadowEnv)) == "" {
		return
	}
	snap := agent.DeliveryIntentShadowSnapshot()
	turns := snap["turns"]
	if turns == 0 {
		return
	}

	pct := func(n int) string {
		return fmt.Sprintf("%5.1f%%", 100*float64(n)/float64(turns))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "delivery intent shadow: %d gated turn(s)\n", turns)
	fmt.Fprintf(&b, "  resolved by tier:\n")
	for _, row := range []struct {
		label string
		key   string
	}{
		{"host state (free)", "resolved_host"},
		{"model declared (free)", "resolved_model"},
		{"classifier (paid)", "resolved_classifier"},
		{"unresolved", "unresolved"},
	} {
		fmt.Fprintf(&b, "    %-22s %5d  %s\n", row.label, snap[row.key], pct(snap[row.key]))
	}
	if n := snap["await_timeouts"]; n > 0 {
		fmt.Fprintf(&b, "  classifier not ready at gate time: %d (await budget too small)\n", n)
	}
	fmt.Fprintf(&b, "  agreement with keyword tables: %d agree / %d disagree\n",
		snap["agree"], snap["disagree"])

	if snap["disagree"] > 0 {
		diffs := map[string]int{
			"task":       snap["task_diff"],
			"mutation":   snap["mutation_diff"],
			"persistent": snap["persistent_diff"],
		}
		keys := make([]string, 0, len(diffs))
		for k := range diffs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, "  disagreements by field:")
		for _, k := range keys {
			fmt.Fprintf(&b, " %s=%d", k, diffs[k])
		}
		b.WriteString("\n")
	}

	fmt.Fprint(os.Stderr, b.String())
}
