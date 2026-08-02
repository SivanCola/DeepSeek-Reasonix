#!/usr/bin/env bash
# Select the newest reviewed Stable record that explicitly promotes a Preview.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/release-activation.sh
source "$script_dir/release-activation.sh"

release_remote="${RELEASE_REMOTE:-origin}"
checkout_sha="$(git rev-parse HEAD^{commit})"
main_sha="$(git ls-remote "$release_remote" refs/heads/main-v2 | awk 'NR == 1 { print $1 }')"
if [ -z "$main_sha" ] || [ "$checkout_sha" != "$main_sha" ]; then
	echo "::error::Stable control checkout must equal current $release_remote/main-v2 ($main_sha), got $checkout_sha" >&2
	exit 1
fi

candidates=()
while IFS= read -r candidate; do
	candidates+=("$candidate")
done < <(node <<'NODE'
const catalog = require("./release-notes/releases.json");
for (const release of catalog.releases) {
  if (release.channel === "stable" && release.status === "reviewed" &&
      release.promotedFrom && release.candidateSha) {
    process.stdout.write(`${release.version}\t${release.promotedFrom}\t${release.candidateSha}\n`);
  }
}
NODE
)

load_preview_event() {
	local preview_tag="$1"
	local dest="$2"
	if [ -n "${RELEASE_PREVIEW_EVENT_STORE:-}" ]; then
		if [ -f "$RELEASE_PREVIEW_EVENT_STORE/$preview_tag/release-event.json" ]; then
			cp "$RELEASE_PREVIEW_EVENT_STORE/$preview_tag/release-event.json" "$dest/release-event.json"
			return 0
		fi
		return 1
	fi
	gh release download "$preview_tag" --pattern release-event.json --dir "$dest" 2>/dev/null
}

preview_event_complete() {
	local preview_tag="$1"
	local expected_sha="$2"
	local event_dir
	event_dir="$(mktemp -d "${TMPDIR:-/tmp}/reasonix-preview-complete.XXXXXX")"
	if ! load_preview_event "$preview_tag" "$event_dir"; then
		rm -rf -- "$event_dir"
		return 1
	fi
	# Structural completeness only: full catalog-bound validation happens later
	# against the frozen control-plane notes during Stable preflight.
	if ! jq -e --arg sha "$expected_sha" --arg id "${preview_tag#v}" '
    .schemaVersion == 1 and
    .releaseId == $id and
    .channel == "preview" and
    .candidateSha == $sha and
    (.publishedAt | type == "string" and test("^\\d{4}-\\d{2}-\\d{2}T")) and
    (.builds.cli | type == "string" and length > 0) and
    (.builds.desktop | type == "string" and length > 0) and
    (.builds.npm | type == "string" and length > 0)
  ' "$event_dir/release-event.json" >/dev/null 2>&1; then
		rm -rf -- "$event_dir"
		return 1
	fi
	rm -rf -- "$event_dir"
	return 0
}

highest_complete_preview() {
	local version="$1"
	local highest=0
	local incomplete=()
	while IFS= read -r ref; do
		local tag="${ref#refs/tags/}"
		if [[ "$tag" =~ ^v${version//./\.}-preview\.([1-9][0-9]*)$ ]]; then
			local number="${BASH_REMATCH[1]}"
			local tag_sha
			tag_sha="$(
				git ls-remote --tags "$release_remote" "refs/tags/$tag" "refs/tags/$tag^{}" |
					awk '/\^\{\}$/ { print $1; found = 1; exit } NR == 1 { first = $1 } END { if (!found) print first }'
			)"
			if [ -n "$tag_sha" ] && preview_event_complete "$tag" "$tag_sha"; then
				if [ "$number" -gt "$highest" ]; then
					highest="$number"
				fi
			else
				incomplete+=("$tag")
			fi
		fi
	done < <(git ls-remote --tags --refs "$release_remote" "refs/tags/v$version-preview.*" | awk '{ print $2 }')
	if [ "${#incomplete[@]}" -gt 0 ]; then
		echo "Incomplete Preview tags (use Request preview recovery): ${incomplete[*]}" >&2
	fi
	echo "$highest"
}

for candidate in "${candidates[@]}"; do
	IFS=$'\t' read -r version promoted_from candidate_sha <<<"$candidate"
	cli_tag="v$version"
	npm_tag="npm-v$version"
	desktop_tag="desktop-v$version"
	preview_tag="v$promoted_from"

	existing=0
	for tag in "$cli_tag" "$npm_tag" "$desktop_tag"; do
		if git ls-remote --exit-code --tags --refs "$release_remote" "refs/tags/$tag" >/dev/null 2>&1; then
			existing=$((existing + 1))
		fi
	done
	if [ "$existing" -eq 3 ]; then
		continue
	fi
	if [ "$existing" -ne 0 ]; then
		echo "::error::Stable $version has a partial tag set; use Request stable recovery after repairing the public identity" >&2
		exit 1
	fi

	if [[ ! "$promoted_from" =~ ^${version//./\.}-preview\.([1-9][0-9]*)$ ]]; then
		echo "::error::$version promotedFrom must be a Preview of the same base version, got: $promoted_from" >&2
		exit 1
	fi
	promoted_number="${BASH_REMATCH[1]}"

	tag_sha="$(
		git ls-remote --tags "$release_remote" "refs/tags/$preview_tag" "refs/tags/$preview_tag^{}" |
			awk '/\^\{\}$/ { print $1; found = 1; exit } NR == 1 { first = $1 } END { if (!found) print first }'
	)"
	if [ -z "$tag_sha" ] || [ "$tag_sha" != "$candidate_sha" ]; then
		echo "::error::$preview_tag resolves to ${tag_sha:-<missing>}, expected catalog candidateSha $candidate_sha" >&2
		exit 1
	fi

	if ! preview_event_complete "$preview_tag" "$candidate_sha"; then
		echo "::error::$preview_tag is incomplete (missing or invalid release-event.json); use Request preview recovery before promotion" >&2
		exit 1
	fi

	highest="$(highest_complete_preview "$version")"
	if [ "$promoted_number" -ne "$highest" ]; then
		echo "::error::$preview_tag is not the newest complete Preview for $version (latest complete is preview.$highest)" >&2
		exit 1
	fi

	git fetch --quiet --no-tags "$release_remote" "refs/tags/$preview_tag"
	if ! git merge-base --is-ancestor "$candidate_sha" "$main_sha"; then
		echo "::error::$preview_tag is not on current main-v2 history" >&2
		exit 1
	fi
	require_promotion_activation_cutoff "$candidate_sha"

	{
		echo "version=$version"
		echo "cli_tag=$cli_tag"
		echo "npm_tag=$npm_tag"
		echo "desktop_tag=$desktop_tag"
		echo "preview_version=$promoted_from"
		echo "preview_tag=$preview_tag"
		echo "sha=$candidate_sha"
	} >>"${GITHUB_OUTPUT:-/dev/stdout}"
	echo "Stable promotion resolved: $preview_tag -> $cli_tag at $candidate_sha"
	exit 0
done

echo "::error::No pending reviewed Stable promotion was found. Prepare Stable notes with promotedFrom and candidateSha first (Actions → Prepare release notes)." >&2
exit 1
