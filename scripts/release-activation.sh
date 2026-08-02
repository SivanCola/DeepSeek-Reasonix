#!/usr/bin/env bash
# Activation cutoff for Preview → Stable promotion proofs.
#
# Rollout:
#   Stage 1 infrastructure PR: leave DEFAULT empty. Shadow validation and
#   fixture tests may run without an ancestry gate.
#   Config-only commit after Stage 1 merges: set DEFAULT to that merge SHA so
#   only later Previews are promotion candidates. Do not flip Stage-3 entrypoints
#   in the same commit.
#   Stage 3 activation PR: keep the filled SHA and enable App-owned entrypoints.
#
# Override with RELEASE_MIN_PROMOTION_CANDIDATE_SHA for local fixtures/tests.
# Set RELEASE_REQUIRE_PROMOTION_CUTOFF=true to fail closed when the cutoff is
# empty (used by Stage-3 production promote paths).
set -euo pipefail

# Empty until the post-Stage-1 config commit records the infrastructure merge SHA.
DEFAULT_MIN_PROMOTION_CANDIDATE_SHA=""

MIN_PROMOTION_CANDIDATE_SHA="${RELEASE_MIN_PROMOTION_CANDIDATE_SHA:-$DEFAULT_MIN_PROMOTION_CANDIDATE_SHA}"
REQUIRE_PROMOTION_CUTOFF="${RELEASE_REQUIRE_PROMOTION_CUTOFF:-false}"

require_promotion_activation_cutoff() {
	local candidate_sha="${1:?candidate SHA is required}"
	if [ -z "$MIN_PROMOTION_CANDIDATE_SHA" ]; then
		if [ "$REQUIRE_PROMOTION_CUTOFF" = "true" ]; then
			echo "::error::promotion activation cutoff is not configured; set DEFAULT_MIN_PROMOTION_CANDIDATE_SHA after the Stage-1 infrastructure merge" >&2
			exit 1
		fi
		echo "promotion activation cutoff is unset; Stage-1 shadow/infrastructure mode allows any main-v2 Preview" >&2
		return 0
	fi
	if [[ ! "$MIN_PROMOTION_CANDIDATE_SHA" =~ ^[0-9a-f]{40}$ ]]; then
		echo "::error::MIN_PROMOTION_CANDIDATE_SHA must be a full commit SHA" >&2
		exit 1
	fi
	if [[ ! "$candidate_sha" =~ ^[0-9a-f]{40}$ ]]; then
		echo "::error::promotion candidate SHA must be a full commit SHA" >&2
		exit 1
	fi
	if ! git cat-file -e "$MIN_PROMOTION_CANDIDATE_SHA^{commit}" 2>/dev/null; then
		git fetch --quiet --no-tags "${RELEASE_REMOTE:-origin}" "$MIN_PROMOTION_CANDIDATE_SHA" 2>/dev/null || true
	fi
	if ! git cat-file -e "$MIN_PROMOTION_CANDIDATE_SHA^{commit}" 2>/dev/null; then
		echo "::error::promotion activation cutoff $MIN_PROMOTION_CANDIDATE_SHA is not available in this checkout" >&2
		exit 1
	fi
	if ! git merge-base --is-ancestor "$MIN_PROMOTION_CANDIDATE_SHA" "$candidate_sha"; then
		echo "::error::candidate $candidate_sha predates the promotion activation cutoff $MIN_PROMOTION_CANDIDATE_SHA" >&2
		exit 1
	fi
}
