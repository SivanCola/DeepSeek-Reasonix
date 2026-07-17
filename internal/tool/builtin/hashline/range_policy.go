// Copyright 2024–2026 xAI and Reasonix contributors.
// SPDX-License-Identifier: Apache-2.0
//
// Modified/adapted from grok-build grok_build_hashline (commit c68e39f).
// See NOTICE and LICENSE-Apache-2.0 in this directory.

// Copyright adapted from xAI grok-build (Apache-2.0), edit/range_policy.rs.
// Modified for Reasonix Hashline v1.

package hashline

import "fmt"

const (
	smallMax  = 5
	mediumMax = 20
)

// RangeWarning returns a caution string for medium (6–20) or large (>20) line
// ranges. start/end are 0-based, end exclusive.
func RangeWarning(start, end int) string {
	count := end - start
	if count < 0 {
		count = 0
	}
	switch {
	case count > mediumMax:
		return fmt.Sprintf(
			"Caution: large range edit (%d lines, lines %d-%d). Verify the target range is correct.",
			count, start+1, end,
		)
	case count > smallMax:
		return fmt.Sprintf(
			"Note: medium range edit (%d lines, lines %d-%d).",
			count, start+1, end,
		)
	default:
		return ""
	}
}
