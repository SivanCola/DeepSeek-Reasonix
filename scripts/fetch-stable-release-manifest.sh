#!/usr/bin/env bash
# Public machine endpoint: no credentials, alternate host, or challenge bypass.
set -euo pipefail
test "$#" -eq 2 || { echo 'usage: fetch-stable-release-manifest.sh VERSION OUTPUT' >&2; exit 2; }
version="$1"
[[ "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || { echo 'Stable manifest probe requires a stable version' >&2; exit 2; }
output="$2"
headers="$(mktemp)"
trap 'rm -f -- "$headers"' EXIT
# Probe with the exact request identity used by the Linux Stable updater. The
# default curl/Node identity receives a Cloudflare challenge on Actions runners.
user_agent="Reasonix-Updater/v$version (linux/amd64; build=stable; update=stable)"
if curl -fsSL --connect-timeout 15 --max-time 45 -A "$user_agent" -D "$headers" https://dl.reasonix.io/latest/latest.json > "$output"; then
	:
else
	status=$?
	echo "Stable manifest observation failed: https://dl.reasonix.io/latest/latest.json (curl exit $status)" >&2
	awk 'tolower($0) ~ /^(http\/|server:|cf-ray:|cf-mitigated:|retry-after:)/ { print }' "$headers" >&2
	exit "$status"
fi
node - "$output" <<'JS'
const fs = require('node:fs');
const manifest = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
if (!/^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(manifest?.version || '')) {
  throw new Error('Public Stable manifest has no valid stable version');
}
JS
