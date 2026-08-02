#!/usr/bin/env bash
# Resolve the next reviewed Preview identity for the current main-v2 commit.
set -euo pipefail

base_version="${BASE_VERSION:?BASE_VERSION is required}"
release_remote="${RELEASE_REMOTE:-origin}"

if [[ ! "$base_version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
	echo "::error::BASE_VERSION must be MAJOR.MINOR.PATCH, got: $base_version" >&2
	exit 1
fi

checkout_sha="$(git rev-parse HEAD^{commit})"
main_sha="$(git ls-remote "$release_remote" refs/heads/main-v2 | awk 'NR == 1 { print $1 }')"
if [ -z "$main_sha" ] || [ "$checkout_sha" != "$main_sha" ]; then
	echo "::error::Preview control checkout must equal current $release_remote/main-v2 ($main_sha), got $checkout_sha" >&2
	exit 1
fi

if git ls-remote --exit-code --tags --refs "$release_remote" "refs/tags/v$base_version" >/dev/null 2>&1; then
	echo "::error::v$base_version is already Stable; choose the next release-train version" >&2
	exit 1
fi

highest=0
while IFS= read -r ref; do
	tag="${ref#refs/tags/}"
	if [[ "$tag" =~ ^v${base_version//./\.}-preview\.([1-9][0-9]*)$ ]] &&
		[ "${BASH_REMATCH[1]}" -gt "$highest" ]; then
		highest="${BASH_REMATCH[1]}"
	fi
done < <(git ls-remote --tags --refs "$release_remote" "refs/tags/v$base_version-preview.*" | awk '{ print $2 }')

preview_number=$((highest + 1))
version="$base_version-preview.$preview_number"
cli_tag="v$version"

RELEASE_VERSION="$version" RELEASE_SHA="$checkout_sha" node <<'NODE'
const catalog = require("./release-notes/releases.json");
const release = catalog.releases.find((entry) => entry.version === process.env.RELEASE_VERSION);
if (!release) throw new Error(`reviewed release notes for ${process.env.RELEASE_VERSION} are missing`);
if (release.channel !== "prerelease" || release.status !== "reviewed") {
  throw new Error(`${process.env.RELEASE_VERSION} must be a reviewed Preview release record`);
}
if (release.baseVersion !== process.env.RELEASE_VERSION.split("-")[0]) {
  throw new Error(`${process.env.RELEASE_VERSION} has the wrong baseVersion`);
}
if (release.candidateSha && release.candidateSha !== process.env.RELEASE_SHA) {
  throw new Error(`${process.env.RELEASE_VERSION} candidateSha does not match current main-v2`);
}
NODE

{
	echo "version=$version"
	echo "base_version=$base_version"
	echo "preview_number=$preview_number"
	echo "cli_tag=$cli_tag"
	echo "desktop_tag=desktop-v$version"
	echo "npm_version=$base_version-canary.$preview_number"
	echo "sha=$checkout_sha"
} >>"${GITHUB_OUTPUT:-/dev/stdout}"

echo "Preview candidate resolved: $cli_tag at $checkout_sha"
