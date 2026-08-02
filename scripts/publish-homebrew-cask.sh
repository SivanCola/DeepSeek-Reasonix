#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?HOMEBREW_TAP_TOKEN is required}"
: "${RELEASE_VERSION:?RELEASE_VERSION is required}"
: "${CASK_PATH:?CASK_PATH is required}"

[[ "$RELEASE_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]
test -s "$CASK_PATH"

repo="esengine/homebrew-reasonix"
path="Casks/reasonix.rb"
meta="$(mktemp)"
current="$(mktemp)"

gh api "repos/$repo/contents/$path" >"$meta"
jq -r .content "$meta" | tr -d '\n' | base64 --decode >"$current"
if cmp -s "$current" "$CASK_PATH"; then
  echo "Homebrew cask already matches $RELEASE_VERSION"
  exit 0
fi

current_version="$(sed -n 's/^  version "\([0-9][0-9.]*\)"$/\1/p' "$current")"
if [ "$current_version" = "$RELEASE_VERSION" ]; then
  echo "::error::Homebrew already names $RELEASE_VERSION but its checksums differ"
  exit 1
fi

content="$(base64 <"$CASK_PATH" | tr -d '\n')"
sha="$(jq -r .sha "$meta")"
gh api --method PUT "repos/$repo/contents/$path" \
  -f message="chore: update Reasonix to v$RELEASE_VERSION" \
  -f sha="$sha" \
  -f content="$content" >/dev/null

published="$(mktemp)"
gh api "repos/$repo/contents/$path" --jq .content | tr -d '\n' | base64 --decode >"$published"
cmp -s "$published" "$CASK_PATH"
