#!/usr/bin/env bash
#
# Build the interface and copy it where the APK will carry it.
#
# The page that ships is the same build the desktop Studio serves — that is the
# whole reason the mobile layout lives in `desktop/frontend-next` rather than in
# an Android project's own web root. Nothing is edited on the way through: the
# only thing this script does that `pnpm build` does not is copy `dist/` into the
# app's assets.
#
# Output: app/src/main/assets/page/
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
android="$here/.."
frontend="$android/../desktop/frontend-next"
dest="$android/app/src/main/assets/page"

if [ ! -d "$frontend" ]; then
  echo "frontend project not found: $frontend" >&2
  exit 1
fi

cd "$frontend"

# pnpm is the package manager the lockfile is written for; npm would resolve a
# different tree than the one that was tested.
if ! command -v pnpm >/dev/null 2>&1; then
  echo "pnpm 10 is required: npm install -g pnpm@10" >&2
  exit 1
fi

if [ ! -d node_modules ]; then
  echo "installing frontend dependencies ..."
  pnpm install --frozen-lockfile
fi

echo "building the frontend ..."
pnpm build

rm -rf "$dest"
mkdir -p "$dest"
cp -R dist/. "$dest/"

if [ ! -f "$dest/index.html" ]; then
  echo "the build produced no index.html; the frontend build probably failed." >&2
  exit 1
fi

count=$(find "$dest" -type f | wc -l)
size=$(du -sk "$dest" | cut -f1)
echo "page ready: $dest ($count files, $((size / 1024)) MiB)"

# MIT requires the copyright notice and the licence text to travel with a binary
# distribution, so the APK carries it. Generated from the repository's own
# `LICENSE` rather than committed a second time: a checked-in copy is one more
# thing that drifts when the root file is updated.
root_license="$android/../LICENSE"
if [ ! -f "$root_license" ]; then
  echo "LICENSE not found: $root_license" >&2
  exit 1
fi
cp "$root_license" "$android/app/src/main/assets/LICENSE"
echo "licence: app/src/main/assets/LICENSE (copied from the repository's LICENSE)"
