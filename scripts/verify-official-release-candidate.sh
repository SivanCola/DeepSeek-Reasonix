#!/usr/bin/env bash
# Fail-closed validation for the single public Reasonix release identity.
set -euo pipefail

version="${RELEASE_VERSION:?RELEASE_VERSION is required}"
tag="${RELEASE_TAG:?RELEASE_TAG is required}"
candidate="${RELEASE_SHA:?RELEASE_SHA is required}"
repository="${RELEASE_REPOSITORY:?RELEASE_REPOSITORY is required}"
allow_existing="${ALLOW_EXISTING_TAG:-false}"
wait_for_ci="${WAIT_FOR_CI:-true}"

if [[ ! "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || [ "$tag" != "v$version" ]; then
	echo "::error::official release identity must be vMAJOR.MINOR.PATCH" >&2
	exit 1
fi
if [[ ! "$candidate" =~ ^[0-9a-f]{40}$ ]]; then
	echo "::error::candidate SHA must be a full commit SHA" >&2
	exit 1
fi
case "$allow_existing:$wait_for_ci" in
true:true | true:false | false:true | false:false) ;;
*) echo "::error::boolean release inputs are invalid" >&2; exit 2 ;;
esac

git fetch origin main-v2 --tags
if ! git merge-base --is-ancestor "$candidate" origin/main-v2; then
	echo "::error::candidate $candidate is not on main-v2 history" >&2
	exit 1
fi
if git show-ref --verify --quiet "refs/tags/$tag"; then
	tag_sha="$(git rev-parse "$tag^{commit}")"
	if [ "$allow_existing" != "true" ] || [ "$tag_sha" != "$candidate" ]; then
		echo "::error::$tag already exists at $tag_sha; expected absent or recovery at $candidate" >&2
		exit 1
	fi
elif [ "$allow_existing" = "true" ]; then
	echo "::error::recovery requires existing tag $tag" >&2
	exit 1
fi

node scripts/release-notes.mjs render --version "$version" --output /tmp/reasonix-official-release-notes.md

if [ "$wait_for_ci" = "true" ]; then
	for attempt in $(seq 1 60); do
		result="$(gh run list --repo "$repository" --workflow ci.yml --commit "$candidate" \
			--json headSha,status,conclusion 2>/dev/null | \
			jq -r --arg sha "$candidate" \
			'[.[] | select(.headSha == $sha and .status == "completed")][0].conclusion // ""' || true)"
		case "$result" in
		success) break ;;
		failure | cancelled | timed_out | action_required | startup_failure)
			echo "::error::CI for $candidate concluded $result" >&2
			exit 1
			;;
		esac
		if [ "$attempt" = 60 ]; then
			echo "::error::timed out waiting for successful CI on $candidate" >&2
			exit 1
		fi
		sleep 10
	done
fi

echo "official release candidate verified: tag=$tag sha=$candidate recovery=$allow_existing"
