#!/usr/bin/env bash
# One-time Stage-C migration: mark historical public prereleases as archived.
set -euo pipefail

repository="${RELEASE_REPOSITORY:-esengine/DeepSeek-Reasonix}"
notice='> **Archived prerelease.** This build is retained for historical compatibility. Install the latest official Reasonix release instead: https://reasonix.io/#start'
tmp="$(mktemp -d "${TMPDIR:-/tmp}/reasonix-archive-releases.XXXXXX")"
trap 'case "$tmp" in */reasonix-archive-releases.*) rm -rf -- "$tmp" ;; esac' EXIT

gh api --paginate "repos/${repository}/releases?per_page=100" --jq \
  '.[] | select(.prerelease == true and (.tag_name | test("^v[0-9]+\\.[0-9]+\\.[0-9]+-preview\\.[0-9]+$"))) | [.tag_name, .body] | @base64' |
while IFS= read -r encoded; do
  row="$(jq -Rr '@base64d' <<<"$encoded")"
  tag="$(jq -r '.[0]' <<<"$row")"
  body="$(jq -r '.[1] // ""' <<<"$row")"
  if grep -Fq '**Archived prerelease.**' <<<"$body"; then
    echo "$tag already archived"
    continue
  fi
  notes="$tmp/${tag//\//-}.md"
  printf '%s\n\n%s\n' "$notice" "$body" >"$notes"
  gh release edit "$tag" --repo "$repository" --notes-file "$notes"
done
