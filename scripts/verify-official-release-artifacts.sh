#!/usr/bin/env bash
# Independent postflight ledger for the one public Reasonix release identity.
set -euo pipefail

repository="${RELEASE_REPOSITORY:?RELEASE_REPOSITORY is required}"
version="${RELEASE_VERSION:?RELEASE_VERSION is required}"
tag="${RELEASE_TAG:?RELEASE_TAG is required}"
sha="${RELEASE_SHA:?RELEASE_SHA is required}"

if [[ ! "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || \
	[ "$tag" != "v$version" ] || [[ ! "$sha" =~ ^[0-9a-f]{40}$ ]]; then
	echo "::error::invalid official release identity" >&2
	exit 1
fi

tmp="$(mktemp -d "${TMPDIR:-/tmp}/reasonix-official-postflight.XXXXXX")"
cleanup() {
	case "$tmp" in */reasonix-official-postflight.*) rm -rf -- "$tmp" ;; *) exit 1 ;; esac
}
trap cleanup EXIT

tag_sha="$(git rev-list -n1 "$tag")"
[ "$tag_sha" = "$sha" ] || { echo "::error::$tag points to $tag_sha, expected $sha"; exit 1; }

gh release view "$tag" --repo "$repository" --json tagName,name,isDraft,isPrerelease,assets >"$tmp/release.json"
jq -e --arg tag "$tag" '
  .tagName == $tag and .name == ("Reasonix " + $tag) and
  .isDraft == false and .isPrerelease == false and
  ([.assets[].name] as $names |
    ["SHA256SUMS", "latest.json", "release-event.json",
     "reasonix-darwin-amd64.tar.gz", "reasonix-darwin-arm64.tar.gz",
     "reasonix-linux-amd64.tar.gz", "reasonix-linux-arm64.tar.gz",
     "reasonix-windows-amd64.zip", "reasonix-windows-arm64.zip",
     "Reasonix-darwin-universal.dmg", "Reasonix-linux-amd64.deb",
     "Reasonix-windows-amd64-installer.exe", "Reasonix-windows-arm64-installer.exe"] |
    all(. as $required | $names | index($required)))
' "$tmp/release.json" >/dev/null
[ "$(gh api "repos/${repository}/releases/latest" --jq .tag_name)" = "$tag" ]

gh release download "$tag" --repo "$repository" --dir "$tmp/assets"
(cd "$tmp/assets" && sha256sum -c SHA256SUMS)
node scripts/release-event.mjs validate --version "$version" --input "$tmp/assets/release-event.json"
jq -e --arg sha "$sha" '.candidateSha == $sha' "$tmp/assets/release-event.json" >/dev/null
jq -e --arg version "$tag" --arg base "https://github.com/${repository}/releases/download/${tag}/" '
  .version == $version and
  ([.platforms[] | .url, .sig] | all(startswith($base)))
' "$tmp/assets/latest.json" >/dev/null
bash scripts/verify-desktop-release-manifest-assets.sh "$tmp/assets/latest.json" "$tmp/assets"
while IFS= read -r url; do
	asset="$tmp/assets/${url##*/}"
	test -s "$asset.minisig"
	go -C desktop run ./cmd/sign verify "$asset"
done < <(jq -r '[.platforms, .native_packages, (.downloads // {})] | map(to_entries) | add | map(.value.url) | unique[]' "$tmp/assets/latest.json")

packages=(
	reasonix
	@reasonix/cli-darwin-arm64 @reasonix/cli-darwin-x64
	@reasonix/cli-linux-arm64 @reasonix/cli-linux-x64
	@reasonix/cli-win32-arm64 @reasonix/cli-win32-x64
)
for package in "${packages[@]}"; do
	metadata="$(npm view "$package@$version" name version reasonixCandidateSha gitHead dist-tags --json)"
	jq -e --arg name "$package" --arg version "$version" --arg sha "$sha" '
    .name == $name and .version == $version and
    ((.reasonixCandidateSha == $sha) or (.gitHead == $sha)) and
    .["dist-tags"].latest == $version and
    .["dist-tags"].canary == $version and
    .["dist-tags"].next == $version
  ' <<<"$metadata" >/dev/null
done

# npm's supported verifier checks both registry signatures and Sigstore
# provenance attestations for the exact installed package set.
npm_audit="$tmp/npm-audit"
mkdir -p "$npm_audit"
(
	cd "$npm_audit"
	npm init --yes >/dev/null
	install_args=(--ignore-scripts --force --package-lock-only)
	for package in "${packages[@]}"; do install_args+=("$package@$version"); done
	npm install "${install_args[@]}" >/dev/null
	npm ci --ignore-scripts --force >/dev/null
	npm audit signatures
)

endpoint="https://${R2_ACCOUNT_ID:?R2_ACCOUNT_ID is required}.r2.cloudflarestorage.com"
aws s3 cp "s3://${R2_BUCKET:?R2_BUCKET is required}/latest/latest.json" "$tmp/r2-latest.json" --endpoint-url "$endpoint"
jq -e --arg version "$tag" --arg base "https://dl.reasonix.io/desktop-${tag}/" '
  .version == $version and ([.platforms[] | .url, .sig] | all(startswith($base)))
' "$tmp/r2-latest.json" >/dev/null
normalize_manifest='walk(if type == "object" and has("url") and has("sig") then del(.url, .sig) else . end)'
jq -S "$normalize_manifest" "$tmp/assets/latest.json" >"$tmp/github-manifest.normalized.json"
jq -S "$normalize_manifest" "$tmp/r2-latest.json" >"$tmp/r2-manifest.normalized.json"
cmp -s "$tmp/github-manifest.normalized.json" "$tmp/r2-manifest.normalized.json"

mkdir -p "$tmp/r2-assets"
while IFS=$'\t' read -r url sig; do
	name="${url##*/}"
	curl --fail --silent --show-error --location --retry 3 "$url" --output "$tmp/r2-assets/$name"
	curl --fail --silent --show-error --location --retry 3 "$sig" --output "$tmp/r2-assets/$name.minisig"
	go -C desktop run ./cmd/sign verify "$tmp/r2-assets/$name"
done < <(jq -r '[.platforms, .native_packages, (.downloads // {})] | map(to_entries) | add | map([.value.url, .value.sig] | @tsv) | unique[]' "$tmp/r2-latest.json")
bash scripts/verify-desktop-release-manifest-assets.sh "$tmp/r2-latest.json" "$tmp/r2-assets"

gh api "repos/esengine/homebrew-reasonix/contents/Casks/reasonix.rb" --jq .content | tr -d '\n' | base64 --decode >"$tmp/homebrew-reasonix.rb"
grep -Eq "version [\"']${version}[\"']" "$tmp/homebrew-reasonix.rb"
test -s "${HOMEBREW_CASK_PATH:?HOMEBREW_CASK_PATH is required}"
cmp -s "$tmp/homebrew-reasonix.rb" "$HOMEBREW_CASK_PATH"

for _ in $(seq 1 30); do
	if curl --fail --silent --show-error --location https://reasonix.io/ --output "$tmp/site.html" && \
		grep -Fq ">v${version}<" "$tmp/site.html"; then
		break
	fi
	sleep 5
done
grep -Fq ">v${version}<" "$tmp/site.html"

jq -n --arg version "$version" --arg tag "$tag" --arg sha "$sha" \
	--arg verifiedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
	'{schemaVersion:1, version:$version, tag:$tag, candidateSha:$sha, verifiedAt:$verifiedAt,
    surfaces:{github:"verified", r2:"verified", npm:"verified", homebrew:"verified", website:"verified"}}' \
	>"$tmp/release-ledger.json"
gh release upload "$tag" "$tmp/release-ledger.json" --repo "$repository" --clobber
cat "$tmp/release-ledger.json" >>"${GITHUB_STEP_SUMMARY:-/dev/null}"
