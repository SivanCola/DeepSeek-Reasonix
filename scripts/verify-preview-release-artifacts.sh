#!/usr/bin/env bash
# Verify the immutable Preview event and every public product surface before promotion.
set -euo pipefail

repository="${RELEASE_REPOSITORY:?RELEASE_REPOSITORY is required}"
preview_tag="${PREVIEW_TAG:?PREVIEW_TAG is required}"
expected_sha="${EXPECTED_SHA:?EXPECTED_SHA is required}"
r2_base="${R2_BASE:-https://dl.reasonix.io}"
gateway_base="${GATEWAY_BASE:-https://crash.reasonix.io/v1/desktop/releases}"
# Daily promotion uses immutable markers and metadata proofs. Set
# PREVIEW_FULL_DOWNLOAD_CHECK=true for the first production cutover.
full_download_check="${PREVIEW_FULL_DOWNLOAD_CHECK:-false}"

if [ "$full_download_check" != "true" ] && [ "$full_download_check" != "false" ]; then
	echo "::error::PREVIEW_FULL_DOWNLOAD_CHECK must be true or false" >&2
	exit 1
fi

if [[ ! "$preview_tag" =~ ^v((0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*))-preview\.([1-9][0-9]*)$ ]]; then
	echo "::error::PREVIEW_TAG must be vMAJOR.MINOR.PATCH-preview.N" >&2
	exit 1
fi
base_version="${BASH_REMATCH[1]}"
preview_number="${BASH_REMATCH[5]}"
preview_version="${preview_tag#v}"
npm_version="$base_version-canary.$preview_number"
desktop_version="desktop-$preview_tag"

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/reasonix-preview-proof.XXXXXX")"
cleanup() {
	case "$tmp_dir" in
	*/reasonix-preview-proof.*) rm -r -- "$tmp_dir" ;;
	*) echo "refusing to clean unexpected Preview proof directory: $tmp_dir" >&2 ;;
	esac
}
trap cleanup EXIT

gh release view "$preview_tag" --repo "$repository" \
	--json isDraft,isPrerelease,assets >"$tmp_dir/release.json"
jq -e '
  .isDraft == false and .isPrerelease == true and
  ([.assets[].name] as $names |
    ["SHA256SUMS", "release-event.json", "reasonix-darwin-amd64.tar.gz",
     "reasonix-darwin-arm64.tar.gz", "reasonix-linux-amd64.tar.gz",
     "reasonix-linux-arm64.tar.gz", "reasonix-windows-amd64.zip",
     "reasonix-windows-arm64.zip"] |
    all(. as $required | $names | index($required)))
' "$tmp_dir/release.json" >/dev/null

gh release download "$preview_tag" --repo "$repository" \
	--pattern SHA256SUMS --pattern release-event.json --dir "$tmp_dir"
node scripts/release-event.mjs validate \
	--version "$preview_version" --input "$tmp_dir/release-event.json"
jq -e --arg sha "$expected_sha" '.candidateSha == $sha and .channel == "preview"' \
	"$tmp_dir/release-event.json" >/dev/null

# Require GitHub asset digests to match SHA256SUMS for every required archive.
while IFS= read -r line; do
	hash="${line%%  *}"
	name="${line#*  }"
	[ -n "$hash" ] && [ -n "$name" ] || continue
	if [[ ! "$hash" =~ ^[0-9a-f]{64}$ ]]; then
		echo "::error::SHA256SUMS contains a non-sha256 digest for $name" >&2
		exit 1
	fi
	digest="$(
		jq -r --arg name "$name" '
      .assets[] | select(.name == $name) | (.digest // empty)
    ' "$tmp_dir/release.json"
	)"
	if [ -z "$digest" ]; then
		echo "::error::CLI asset $name is missing a GitHub digest" >&2
		exit 1
	fi
	expected_digest="sha256:$hash"
	if [ "$digest" != "$expected_digest" ]; then
		echo "::error::CLI asset $name digest $digest does not match SHA256SUMS $expected_digest" >&2
		exit 1
	fi
done <"$tmp_dir/SHA256SUMS"

for asset in \
	SHA256SUMS \
	reasonix-darwin-amd64.tar.gz \
	reasonix-darwin-arm64.tar.gz \
	reasonix-linux-amd64.tar.gz \
	reasonix-linux-arm64.tar.gz \
	reasonix-windows-amd64.zip \
	reasonix-windows-arm64.zip; do
	grep -Eq "  ${asset//./\\.}$" "$tmp_dir/SHA256SUMS" || {
		echo "::error::SHA256SUMS is missing required asset $asset" >&2
		exit 1
	}
done

npm view "reasonix@$npm_version" \
	version gitHead reasonixCandidateSha optionalDependencies --json >"$tmp_dir/npm-root.json"
jq -e --arg version "$npm_version" --arg sha "$expected_sha" '
  .version == $version and .gitHead == $sha and .reasonixCandidateSha == $sha and
  (.optionalDependencies | type == "object" and length == 6 and
   all(to_entries[]; .value == $version))
' "$tmp_dir/npm-root.json" >/dev/null
while IFS= read -r package_name; do
	npm view "$package_name@$npm_version" version gitHead reasonixCandidateSha --json \
		>"$tmp_dir/npm-package.json"
	jq -e --arg version "$npm_version" --arg sha "$expected_sha" '
    .version == $version and .gitHead == $sha and .reasonixCandidateSha == $sha
  ' "$tmp_dir/npm-package.json" >/dev/null
done < <(jq -r '.optionalDependencies | keys[]' "$tmp_dir/npm-root.json")

first_manifest=""
urls_file="$tmp_dir/urls.txt"
: >"$urls_file"
for entry in \
	"preview|$r2_base/preview/latest.json" \
	"canary|$r2_base/canary/latest.json" \
	"gateway|$gateway_base/preview/latest.json" \
	"immutable|$r2_base/$desktop_version/latest.json"; do
	label="${entry%%|*}"
	url="${entry#*|}"
	manifest="$tmp_dir/$label.json"
	status="$(curl -sSLo "$manifest" -w '%{http_code}' "$url")"
	[ "$status" = "200" ] || {
		echo "::error::$label Preview manifest returned HTTP $status" >&2
		exit 1
	}
	jq -e --arg version "v$preview_version" '
    .version == $version and
    ((.platforms // {}) + (.native_packages // {}) + (.downloads // {})) as $items |
    ($items | length) > 0 and
    all($items[];
      (.url | type == "string" and length > 0) and
      (.sig | type == "string" and length > 0) and
      (.sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
      (.size | type == "number" and . > 0))
  ' "$manifest" >/dev/null
	jq -r '
    ((.platforms // {}) + (.native_packages // {}) + (.downloads // {}))[] |
    .url, .sig
  ' "$manifest" >>"$urls_file"
	if [ -z "$first_manifest" ]; then
		first_manifest="$manifest"
	else
		cmp -s "$first_manifest" "$manifest" || {
			echo "::error::$label Preview manifest differs from the immutable manifest" >&2
			exit 1
		}
	fi
done

[ -s "$urls_file" ] || {
	echo "::error::Preview manifest URL extraction is empty" >&2
	exit 1
}
sort -u "$urls_file" -o "$urls_file"
if [ "$full_download_check" = "true" ]; then
	desktop_assets="$tmp_dir/desktop-assets"
	signature_verifier="$tmp_dir/reasonix-desktop-sign"
	mkdir -p "$desktop_assets"
	go -C desktop build -o "$signature_verifier" ./cmd/sign
	bash scripts/verify-preview-desktop-downloads.sh \
		"$first_manifest" "$desktop_assets" "$signature_verifier"
else
	while IFS= read -r url; do
		[ -n "$url" ] || {
			echo "::error::Preview manifest contains an empty asset URL" >&2
			exit 1
		}
		status="$(curl -sSIL -o /dev/null -w '%{http_code}' "$url")"
		[ "$status" = "200" ] || {
			echo "::error::Preview asset returned HTTP $status: $url" >&2
			exit 1
		}
	done <"$urls_file"
fi

echo "Preview public proof verified: $preview_tag at $expected_sha"
