#!/usr/bin/env bash
# Create one Preview tag or the three Stable tags as one immutable operation.
set -euo pipefail

candidate_sha="${RELEASE_SHA:?RELEASE_SHA is required}"
release_remote="${RELEASE_REMOTE:-origin}"
read -r -a release_tags <<<"${RELEASE_TAGS:?RELEASE_TAGS is required}"

if [[ ! "$candidate_sha" =~ ^[0-9a-f]{40}$ ]]; then
	echo "::error::RELEASE_SHA must be a full commit SHA" >&2
	exit 1
fi
if [ "${#release_tags[@]}" -eq 1 ]; then
	[[ "${release_tags[0]}" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-preview\.([1-9][0-9]*)$ ]] || {
		echo "::error::single-tag creation is reserved for a canonical Preview tag" >&2
		exit 1
	}
elif [ "${#release_tags[@]}" -eq 3 ]; then
	[[ "${release_tags[0]}" =~ ^v((0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*))$ ]] || {
		echo "::error::the first Stable tag must be vMAJOR.MINOR.PATCH" >&2
		exit 1
	}
	version="${BASH_REMATCH[1]}"
	[ "${release_tags[1]}" = "npm-v$version" ] && [ "${release_tags[2]}" = "desktop-v$version" ] || {
		echo "::error::Stable tags must be ordered as v$version npm-v$version desktop-v$version" >&2
		exit 1
	}
else
	echo "::error::RELEASE_TAGS must contain one Preview tag or three Stable tags" >&2
	exit 1
fi

git cat-file -e "$candidate_sha^{commit}"
main_sha="$(git ls-remote "$release_remote" refs/heads/main-v2 | awk 'NR == 1 { print $1 }')"
git fetch --quiet --no-tags "$release_remote" refs/heads/main-v2
if [ -z "$main_sha" ] || ! git merge-base --is-ancestor "$candidate_sha" "$main_sha"; then
	echo "::error::release candidate $candidate_sha is not on $release_remote/main-v2 history" >&2
	exit 1
fi

refspecs=()
for tag in "${release_tags[@]}"; do
	if git ls-remote --exit-code --tags --refs "$release_remote" "refs/tags/$tag" >/dev/null 2>&1; then
		echo "::error::release tag already exists: $tag" >&2
		exit 1
	fi
	refspecs+=("$candidate_sha:refs/tags/$tag")
done

git push --atomic "$release_remote" "${refspecs[@]}"
for tag in "${release_tags[@]}"; do
	remote_sha="$(git ls-remote --tags --refs "$release_remote" "refs/tags/$tag" | awk 'NR == 1 { print $1 }')"
	if [ "$remote_sha" != "$candidate_sha" ]; then
		echo "::error::$tag read back as ${remote_sha:-<missing>}, expected $candidate_sha" >&2
		exit 1
	fi
done

echo "Created release tags at $candidate_sha: ${release_tags[*]}"
