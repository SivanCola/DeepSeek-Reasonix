#!/usr/bin/env bash
# A failed read or an older pointer cannot prove that a newer release owns the site.
set -euo pipefail

if [ "$#" -ne 2 ]; then
	echo "usage: observe-release-site.sh VERSION publish|recover" >&2
	exit 2
fi
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
manifest="$(mktemp)"
headers="$(mktemp)"
trap 'rm -f -- "$manifest" "$headers"' EXIT
if curl -fsSL -D "$headers" https://dl.reasonix.io/latest/latest.json > "$manifest"; then
	:
else
	status=$?
	echo "Stable manifest observation failed: https://dl.reasonix.io/latest/latest.json (curl exit $status)" >&2
	awk 'tolower($0) ~ /^(http\/|server:|cf-ray:|cf-mitigated:|retry-after:)/ { print }' "$headers" >&2
	exit "$status"
fi
node "$script_dir/release-publication-ledger.mjs" site-owner "$1" "$2" "$manifest"
