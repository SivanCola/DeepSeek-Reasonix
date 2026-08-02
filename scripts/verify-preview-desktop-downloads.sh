#!/usr/bin/env bash
# Download and authenticate every Desktop payload referenced by a Preview manifest.
set -euo pipefail

manifest="${1:-}"
download_dir="${2:-}"
signature_verifier="${3:-}"

if [ ! -f "$manifest" ] || [ ! -d "$download_dir" ] || [ ! -x "$signature_verifier" ]; then
	echo "usage: $0 MANIFEST DOWNLOAD_DIRECTORY SIGNATURE_VERIFIER" >&2
	exit 2
fi
if [ -n "$(find "$download_dir" -mindepth 1 -maxdepth 1 -print -quit)" ]; then
	echo "Preview download directory must be empty: $download_dir" >&2
	exit 1
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
entries="$(mktemp "${TMPDIR:-/tmp}/reasonix-preview-downloads.XXXXXX")"
download_map="$(mktemp "${TMPDIR:-/tmp}/reasonix-preview-download-map.XXXXXX")"
payload_names="$(mktemp "${TMPDIR:-/tmp}/reasonix-preview-payloads.XXXXXX")"
cleanup_preview_download_metadata() {
	rm -f -- "$entries" "$download_map" "$payload_names"
}
trap cleanup_preview_download_metadata EXIT

jq -er '
  [(.platforms // {}), (.native_packages // {}), (.downloads // {})]
  | map(to_entries) | add[]
  | [.value.url, .value.sig]
  | @tsv
' "$manifest" >"$entries"
[ -s "$entries" ] || {
	echo "::error::Preview manifest has no downloadable Desktop assets" >&2
	exit 1
}

download_asset() {
	local url="$1"
	local name existing_url status
	name="${url##*/}"
	if [[ ! "$name" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]]; then
		echo "::error::Preview manifest has an unsafe asset URL: $url" >&2
		exit 1
	fi
	existing_url="$(awk -F '\t' -v name="$name" '$1 == name { print $2; exit }' "$download_map")"
	if [ -n "$existing_url" ]; then
		if [ "$existing_url" != "$url" ]; then
			echo "::error::Preview manifest maps multiple URLs to asset name $name" >&2
			exit 1
		fi
		return
	fi
	if ! status="$(curl --fail --location --silent --show-error \
		--output "$download_dir/$name" --write-out '%{http_code}' "$url")"; then
		echo "::error::Preview asset download failed: $url" >&2
		exit 1
	fi
	if [ "$status" != "200" ] || [ ! -s "$download_dir/$name" ]; then
		echo "::error::Preview asset download failed or was empty (HTTP $status): $url" >&2
		exit 1
	fi
	printf '%s\t%s\n' "$name" "$url" >>"$download_map"
}

while IFS=$'\t' read -r url signature_url; do
	[ -n "$url" ] && [ -n "$signature_url" ] || {
		echo "::error::Preview manifest contains an empty payload or signature URL" >&2
		exit 1
	}
	if [ "$signature_url" != "$url.minisig" ]; then
		echo "::error::Preview signature URL does not match payload URL: $url" >&2
		exit 1
	fi
	download_asset "$url"
	download_asset "$signature_url"
	printf '%s\n' "${url##*/}" >>"$payload_names"
done <"$entries"

bash "$script_dir/verify-desktop-release-manifest-assets.sh" "$manifest" "$download_dir"
sort -u "$payload_names" -o "$payload_names"
while IFS= read -r payload_name; do
	"$signature_verifier" verify "$download_dir/$payload_name"
done <"$payload_names"

echo "Preview Desktop payload hashes, sizes, and signatures verified"
