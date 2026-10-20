#!/usr/bin/env bash
# Builds every task's workdir/ from one pinned commit of this repository plus
# the fixture-authored files in base/ and overlay/<task-id>/. With --check it
# rebuilds into a scratch directory and fails if any committed workdir differs.
set -euo pipefail

PIN=42636ddcb425b7af07abd88397d02c1cfe3adcf5
PATHS=(
	internal/contract/pricing
	internal/contract/ablation
	internal/contract/agentgraph
	internal/base/retrieval
	internal/base/rulelist
	internal/base/tokencount
	internal/base/neterr
	internal/base/nilutil
)

here="$(cd "$(dirname "$0")" && pwd)"
repo="$(git -C "$here" rev-parse --show-toplevel)"

build() {
	local id="$1" dest="$2"
	rm -rf "$dest"
	mkdir -p "$dest"
	git -C "$repo" archive "$PIN" -- "${PATHS[@]}" | tar -x -C "$dest"
	cp -R "$here/base/." "$dest/"
	if [ -d "$here/overlay/$id" ]; then
		cp -R "$here/overlay/$id/." "$dest/"
	fi
}

check=0
[ "${1:-}" = "--check" ] && check=1

status=0
for task in "$here"/tasks/*/; do
	id="$(basename "$task")"
	if [ "$check" = 1 ]; then
		scratch="$(mktemp -d)"
		build "$id" "$scratch/workdir"
		if ! diff -r "$scratch/workdir" "$task/workdir" >/dev/null; then
			echo "$id: workdir/ differs from the pinned snapshot; run $0" >&2
			diff -rq "$scratch/workdir" "$task/workdir" >&2 || true
			status=1
		fi
		rm -rf "$scratch"
	else
		build "$id" "$task/workdir"
	fi
done
exit "$status"
