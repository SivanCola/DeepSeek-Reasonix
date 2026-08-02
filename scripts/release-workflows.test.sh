#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
publish="$root/.github/workflows/release-stable.yml"
recover="$root/.github/workflows/recover-release.yml"
prepare="$root/.github/workflows/prepare-release-notes.yml"

grep -Fxq 'name: Prepare release' "$prepare"
grep -Fxq 'name: Publish release' "$publish"
grep -Fxq 'name: Recover release' "$recover"
[ "$(grep -l 'workflow_dispatch:' "$root/.github/workflows"/release*.yml "$prepare" "$recover" | wc -l | tr -d ' ')" = 2 ]

# Developer flow: one version input, one notes PR, one environment gate.
[ "$(sed -n '/workflow_dispatch:/,/permissions:/p' "$prepare" | grep -Ec '^      [a-z_]+:$')" = 1 ]
grep -Eq '^    environment: release$' "$publish"
[ "$(grep -Ec '^    environment: release$' "$publish")" = 1 ]
grep -Fq 'RELEASE_TAGGER_APP_ID' "$publish"
grep -Fq 'RELEASE_TAGGER_PRIVATE_KEY' "$publish"
grep -Fq 'actions/create-github-app-token@' "$publish"

# Exact candidate validation runs before and after approval; private candidates
# are retained for seven days and no tag exists before authorize.
[ "$(grep -Fc 'verify-official-release-candidate.sh' "$publish")" = 2 ]
grep -Fq 'retention-days: 7' "$publish"
candidate_line="$(grep -n -m1 '^  candidate:' "$publish" | cut -d: -f1)"
authorize_line="$(grep -n -m1 '^  authorize:' "$publish" | cut -d: -f1)"
tag_line="$(grep -n -m1 'git/refs' "$publish" | cut -d: -f1)"
[ "$candidate_line" -lt "$authorize_line" ]
[ "$authorize_line" -lt "$tag_line" ]

# One tag feeds every publisher. Child workflows are reusable only and cannot
# expose extra approval or manual publication entrypoints.
grep -Fq 'tag: ${{ needs.authorize.outputs.tag }}' "$publish"
for child in release.yml release-desktop.yml release-npm.yml; do
  section="$(sed -n '/^on:/,/^permissions:/p' "$root/.github/workflows/$child")"
  grep -q 'workflow_call:' <<<"$section"
  if grep -Eq 'workflow_dispatch:|push:' <<<"$section"; then
    echo "$child must be callable only" >&2
    exit 1
  fi
  ! grep -Eq '^    environment:' "$root/.github/workflows/$child"
  grep -Fq 'test "${{ inputs.orchestrated }}" = "true"' "$root/.github/workflows/$child"
done

# npm and R2 remain staged until official postflight advances public pointers.
grep -Fq -- '--publish --stage-only' "$root/.github/workflows/release-npm.yml"
grep -Fq 'finalize-npm-official-release.mjs' "$publish"
grep -Fq 'defer_pointer: true' "$publish"
grep -Fq 'storage_tag=desktop-$version' "$root/scripts/resolve-desktop-release.sh"
grep -Fq 'desktop-v${VERSION}/latest.json' "$publish"

# The unified GitHub Release is published only after all staged publishers and
# receives both the immutable event and independent postflight ledger.
postflight_line="$(grep -n -m1 '^  postflight:' "$publish" | cut -d: -f1)"
publish_line="$(grep -n -m1 'gh release edit.*--draft=false' "$publish" | cut -d: -f1)"
[ "$postflight_line" -lt "$publish_line" ]
grep -Fq 'release-event.mjs generate' "$publish"
grep -Fq 'verify-official-release-artifacts.sh' "$publish"
grep -Fq 'release-ledger.json' "$root/scripts/verify-official-release-artifacts.sh"
grep -Fq 'skip_upload: true' "$root/.goreleaser.yaml"
grep -Fq 'publish-homebrew-cask.sh' "$publish"
node --test "$root/scripts/render-homebrew-cask.test.mjs"
node --test "$root/scripts/finalize-npm-official-release.test.mjs"

# Removed public channel orchestrators and tag relays cannot reactivate.
for retired in release-preview.yml release-stable-trigger.yml release-cli-trigger.yml release-desktop-trigger.yml; do
  test ! -e "$root/.github/workflows/$retired"
done

bash -n \
  "$root/scripts/verify-official-release-candidate.sh" \
  "$root/scripts/verify-official-release-artifacts.sh" \
  "$root/scripts/publish-homebrew-cask.sh" \
  "$root/scripts/resolve-desktop-release.sh" \
  "$root/scripts/resolve-desktop-candidate.sh" \
  "$root/scripts/resolve-npm-release.sh"
node --check "$root/scripts/resolve-official-release.mjs"
node --check "$root/scripts/finalize-npm-official-release.mjs"

echo "release workflow contracts: PASS"
