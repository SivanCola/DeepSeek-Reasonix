#!/usr/bin/env bash
# Re-check an approved release identity immediately before tag creation.
#
# Modes:
#   MODE=preview  RELEASE_TAGS="vX.Y.Z-preview.N" RELEASE_SHA=... VERSION=...
#   MODE=stable   RELEASE_TAGS="vX.Y.Z npm-vX.Y.Z desktop-vX.Y.Z" RELEASE_SHA=...
#                 VERSION=... PREVIEW_VERSION=... PREVIEW_TAG=...
set -euo pipefail

mode="${MODE:?MODE is required}"
candidate_sha="${RELEASE_SHA:?RELEASE_SHA is required}"
version="${VERSION:?VERSION is required}"
release_remote="${RELEASE_REMOTE:-origin}"
read -r -a release_tags <<<"${RELEASE_TAGS:?RELEASE_TAGS is required}"

if [[ ! "$candidate_sha" =~ ^[0-9a-f]{40}$ ]]; then
	echo "::error::RELEASE_SHA must be a full commit SHA" >&2
	exit 1
fi

git cat-file -e "$candidate_sha^{commit}"
main_sha="$(git ls-remote "$release_remote" refs/heads/main-v2 | awk 'NR == 1 { print $1 }')"
if [ -z "$main_sha" ]; then
	echo "::error::cannot resolve $release_remote/main-v2" >&2
	exit 1
fi
git fetch --quiet --no-tags "$release_remote" refs/heads/main-v2
if ! git merge-base --is-ancestor "$candidate_sha" "$main_sha"; then
	echo "::error::approved candidate $candidate_sha is no longer on $release_remote/main-v2 history" >&2
	exit 1
fi

for tag in "${release_tags[@]}"; do
	if git ls-remote --exit-code --tags --refs "$release_remote" "refs/tags/$tag" >/dev/null 2>&1; then
		echo "::error::target release tag already exists before creation: $tag" >&2
		exit 1
	fi
done

case "$mode" in
preview)
	if [[ ! "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-preview\.([1-9][0-9]*)$ ]]; then
		echo "::error::Preview VERSION is invalid: $version" >&2
		exit 1
	fi
	[ "${#release_tags[@]}" -eq 1 ] && [ "${release_tags[0]}" = "v$version" ] || {
		echo "::error::Preview revalidation expects RELEASE_TAGS=v$version" >&2
		exit 1
	}
	RELEASE_VERSION="$version" RELEASE_SHA="$candidate_sha" node <<'NODE'
const catalog = require("./release-notes/releases.json");
const release = catalog.releases.find((entry) => entry.version === process.env.RELEASE_VERSION);
if (!release) throw new Error(`reviewed release notes for ${process.env.RELEASE_VERSION} are missing`);
if (release.channel !== "prerelease" || release.status !== "reviewed") {
  throw new Error(`${process.env.RELEASE_VERSION} must remain a reviewed Preview record`);
}
if (release.candidateSha && release.candidateSha !== process.env.RELEASE_SHA) {
  throw new Error(`${process.env.RELEASE_VERSION} candidateSha no longer matches the approved SHA`);
}
NODE
	;;
stable)
	if [[ ! "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
		echo "::error::Stable VERSION is invalid: $version" >&2
		exit 1
	fi
	preview_version="${PREVIEW_VERSION:?PREVIEW_VERSION is required}"
	preview_tag="${PREVIEW_TAG:?PREVIEW_TAG is required}"
	[ "${#release_tags[@]}" -eq 3 ] || {
		echo "::error::Stable revalidation expects three tags" >&2
		exit 1
	}
	[ "${release_tags[0]}" = "v$version" ] &&
		[ "${release_tags[1]}" = "npm-v$version" ] &&
		[ "${release_tags[2]}" = "desktop-v$version" ] || {
		echo "::error::Stable tags must be v$version npm-v$version desktop-v$version" >&2
		exit 1
	}
	[ "$preview_tag" = "v$preview_version" ] || {
		echo "::error::PREVIEW_TAG must equal v$PREVIEW_VERSION" >&2
		exit 1
	}
	RELEASE_VERSION="$version" PREVIEW_VERSION="$preview_version" RELEASE_SHA="$candidate_sha" node <<'NODE'
const catalog = require("./release-notes/releases.json");
const release = catalog.releases.find((entry) => entry.version === process.env.RELEASE_VERSION);
if (!release) throw new Error(`reviewed release notes for ${process.env.RELEASE_VERSION} are missing`);
if (release.channel !== "stable" || release.status !== "reviewed") {
  throw new Error(`${process.env.RELEASE_VERSION} must remain a reviewed Stable record`);
}
if (release.promotedFrom !== process.env.PREVIEW_VERSION) {
  throw new Error(`${process.env.RELEASE_VERSION} promotedFrom no longer matches the approved Preview`);
}
if (release.candidateSha !== process.env.RELEASE_SHA) {
  throw new Error(`${process.env.RELEASE_VERSION} candidateSha no longer matches the approved SHA`);
}
NODE
	tag_sha="$(
		git ls-remote --tags "$release_remote" "refs/tags/$preview_tag" "refs/tags/$preview_tag^{}" |
			awk '/\^\{\}$/ { print $1; found = 1; exit } NR == 1 { first = $1 } END { if (!found) print first }'
	)"
	if [ -z "$tag_sha" ] || [ "$tag_sha" != "$candidate_sha" ]; then
		echo "::error::$preview_tag resolves to ${tag_sha:-<missing>}, expected approved $candidate_sha" >&2
		exit 1
	fi
	# Reject when a newer complete Preview for the same base version appeared.
	highest_complete=0
	while IFS= read -r ref; do
		tag="${ref#refs/tags/}"
		if [[ "$tag" =~ ^v${version//./\.}-preview\.([1-9][0-9]*)$ ]]; then
			number="${BASH_REMATCH[1]}"
			event_dir="$(mktemp -d "${TMPDIR:-/tmp}/reasonix-revalidate-preview.XXXXXX")"
			event_ok=false
			if [ -n "${RELEASE_PREVIEW_EVENT_STORE:-}" ] &&
				[ -f "$RELEASE_PREVIEW_EVENT_STORE/$tag/release-event.json" ]; then
				cp "$RELEASE_PREVIEW_EVENT_STORE/$tag/release-event.json" "$event_dir/release-event.json"
				event_ok=true
			elif gh release download "$tag" --pattern release-event.json --dir "$event_dir" 2>/dev/null; then
				event_ok=true
			fi
			tag_sha="$(
				git ls-remote --tags "$release_remote" "refs/tags/$tag" "refs/tags/$tag^{}" |
					awk '/\^\{\}$/ { print $1; found = 1; exit } NR == 1 { first = $1 } END { if (!found) print first }'
			)"
			if [ "$event_ok" = true ] &&
				jq -e --arg sha "$tag_sha" --arg id "${tag#v}" '
          .schemaVersion == 1 and
          .releaseId == $id and
          .channel == "preview" and
          .candidateSha == $sha and
          (.publishedAt | type == "string" and test("^\\d{4}-\\d{2}-\\d{2}T")) and
          (.builds.cli | type == "string" and length > 0) and
          (.builds.desktop | type == "string" and length > 0) and
          (.builds.npm | type == "string" and length > 0)
        ' "$event_dir/release-event.json" >/dev/null 2>&1; then
				if [ "$number" -gt "$highest_complete" ]; then
					highest_complete="$number"
				fi
			fi
			rm -rf -- "$event_dir"
		fi
	done < <(git ls-remote --tags --refs "$release_remote" "refs/tags/v$version-preview.*" | awk '{ print $2 }')
	approved_number="${preview_version##*-preview.}"
	if [ "$highest_complete" -gt 0 ] && [ "$approved_number" -ne "$highest_complete" ]; then
		echo "::error::$preview_tag is no longer the newest complete Preview for $version (latest complete is preview.$highest_complete); use Request preview recovery for incomplete tags" >&2
		exit 1
	fi
	;;
*)
	echo "::error::unknown MODE: $mode" >&2
	exit 1
	;;
esac

echo "Revalidated $mode release candidate $version at $candidate_sha"
