#!/usr/bin/env bash
set -euo pipefail

repository="${RELEASE_REPOSITORY:?RELEASE_REPOSITORY is required}"
tag="${RELEASE_TAG:?RELEASE_TAG is required}"
version="${RELEASE_VERSION:?RELEASE_VERSION is required}"
[ "$tag" = "v$version" ] || { echo "::error::CLI metadata identity mismatch"; exit 1; }
endpoint="https://${R2_ACCOUNT_ID:?R2_ACCOUNT_ID is required}.r2.cloudflarestorage.com"
bucket="${R2_BUCKET:?R2_BUCKET is required}"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/reasonix-cli-metadata.XXXXXX")"
trap 'case "$tmp" in */reasonix-cli-metadata.*) rm -rf -- "$tmp" ;; esac' EXIT

required='["reasonix-darwin-amd64.tar.gz","reasonix-darwin-arm64.tar.gz","reasonix-linux-amd64.tar.gz","reasonix-linux-arm64.tar.gz","reasonix-windows-amd64.zip","reasonix-windows-arm64.zip","SHA256SUMS"]'
gh api "repos/${repository}/releases/tags/$tag" >"$tmp/raw.json"
jq --arg tag "$tag" --argjson required "$required" '
  if .tag_name != $tag or .draft or .prerelease then error("release is not the published official release") else . end |
  . as $release | ($release.assets | map({key:.name,value:.}) | from_entries) as $assets |
  if ($required | all(. as $name | $assets[$name] != null)) then {
    tag_name:$tag, prerelease:false, html_url:$release.html_url,
    release_notes_url:("https://reasonix.io/changelog/" + $tag + "/"),
    assets:[$required[] as $name | $assets[$name] | {name,browser_download_url,size}]
  } else error("missing CLI assets") end
' "$tmp/raw.json" >"$tmp/candidate.json"
bash scripts/validate-cli-release-manifest.sh stable "$tag" "$repository" "$tmp/candidate.json" "$tag"

immutable="cli/releases/${tag}/latest.json"
if aws s3 cp "s3://${bucket}/${immutable}" "$tmp/immutable.json" --endpoint-url "$endpoint" 2>/dev/null; then
  bash scripts/validate-cli-release-manifest.sh legacy-stable "$tag" "$repository" "$tmp/immutable.json" "$tag"
  bash scripts/compare-cli-release-manifests.sh "$tmp/candidate.json" "$tmp/immutable.json"
else
  aws s3 cp "$tmp/candidate.json" "s3://${bucket}/${immutable}" --endpoint-url "$endpoint" --content-type "application/json; charset=utf-8" --cache-control "public, max-age=31536000, immutable"
fi

current=-
if aws s3 cp "s3://${bucket}/cli/stable/latest.json" "$tmp/current.json" --endpoint-url "$endpoint" 2>/dev/null; then current="$tmp/current.json"; fi
decision="$(bash scripts/decide-cli-pointer-update.sh stable "$tmp/candidate.json" "$current")"
if [ "$decision" = update ]; then
  aws s3 cp "$tmp/candidate.json" "s3://${bucket}/cli/stable/latest.json" --endpoint-url "$endpoint" --content-type "application/json; charset=utf-8" --cache-control "public, max-age=300, stale-if-error=86400"
elif [ "$decision" != skip ]; then
  echo "::error::invalid CLI pointer decision: $decision"; exit 1
fi
