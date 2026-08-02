# Releasing Reasonix

Reasonix has one user-facing release line. Every new version has one immutable
candidate commit, one `vX.Y.Z` tag, and one GitHub Release containing CLI and
Desktop assets, `SHA256SUMS`, signatures, `latest.json`, `release-event.json`,
and the postflight ledger. npm, R2, Homebrew, and reasonix.io must resolve to
that same version and candidate.

## Daily release flow

The developer interaction budget is one version input, one release-notes PR
merge, and one environment approval:

1. Open Actions → **Prepare release**.
2. Enter `X.Y.Z`. Do not create a tag.
3. Review and merge the generated bilingual release-notes PR.
4. Wait for exact-commit CI and the private candidate build.
5. Approve the single `release` environment deployment.
6. Monitor **Publish release** through postflight.

The workflow freezes the notes merge SHA as the product candidate, revalidates
it after approval, and uses the repository-scoped `reasonix-release-tagger` App
to create the only public tag. No child publisher or SignPath request may ask
for another human approval.

## Automated publication

Before approval, Actions builds private CLI and npm candidates and retains them
for seven days. It does not create a public tag, Release, R2 pointer, or npm
prerelease. After approval it:

1. creates `vX.Y.Z` with the App;
2. stages CLI and signed Desktop assets in one draft GitHub Release;
3. uploads Desktop assets to immutable R2 `desktop-vX.Y.Z/`;
4. publishes npm packages under the temporary `official-staging` dist-tag;
5. verifies package provenance, checksums, manifests, and signatures;
6. advances npm `latest`, compatibility `canary` and `next`, and R2
   `latest/latest.json`;
7. publishes the GitHub Release as latest and verifies Homebrew;
8. attaches `release-event.json` and `release-ledger.json`, then refreshes the
   website publication marker.

`canary` and `next` are compatibility aliases only. They always point to the
same official npm version as `latest` and are not a testing channel.

## Recovery

Open Actions → **Recover release**, enter an existing `vX.Y.Z`, and approve the
same `release` environment once. Recovery reuses the tag and candidate SHA,
fills only missing assets or packages, and reruns postflight. It must fail
closed if an existing checksum, signature, manifest, package provenance, or
immutable R2 object belongs to different content. Tags are never moved or
deleted; product regressions ship as a higher patch version.

## Repository controls

- The `reasonix-release-tagger` App is installed only on this repository with
  `contents: write`. Its App ID and private key live only in `release`.
- Rulesets deny update and deletion of `v*`, historical `desktop-v*`, and
  `npm-v*`. Only the App may create new `v*` tags.
- Humans and normal workflow tokens cannot create normal release tags.
- `release` has one reviewer group. The retired `canary` environment and its
  secrets are removed only after the bridge release is verified.

## Migration and historical compatibility

The final bridge build is published with the old system before this workflow is
activated. Its former rolling endpoints remain frozen so older clients can
reach that bridge and then the official release. New clients ignore legacy
channel settings, while accepting historical and unified GitHub asset bases.
Historical prerelease tags, Releases, and exact changelog pages are retained as
archives; they are never reused or deleted.

Activation must be atomic: enable **Publish release**, switch the tag ruleset to
App-only creation, remove old relays and recovery entrypoints, and publish the
website/documentation changes in the same window.

## Rollout sequence

Do not merge the final activation as one unphased change:

1. **Runtime bridge:** merge the CLI/Desktop compatibility layer while the old
   release workflows still exist. Re-check remote tags, then use the next free
   `1.19.2-preview.N` identity (the initial expected value is
   `1.19.2-preview.1`) for the final bridge build.
2. **Bridge official release:** publish `v1.19.2` with the old system and prove
   old CLI/Desktop installations can traverse bridge → official. Freeze the
   old rolling endpoints at the bridge and align npm compatibility tags.
3. **Engine and settings:** merge the hidden single-release engine, configure
   the App, `release` secrets, SignPath definitions, and compatibility tag
   rules. Run private candidates without moving public state.
4. **Atomic activation:** enable the new workflows, switch `v*` creation to the
   App, remove old relays, and deploy the website/docs in one window.
   Run `bash scripts/archive-preview-releases.sh` once with an authenticated
   maintainer CLI to add the archive banner without deleting historical assets.
5. **First native release:** publish the next patch (normally `v1.19.3`) and
   complete the acceptance ledger before removing the retired environment.

PR #7194 is superseded and must not be re-opened or activated. Only its
exact-SHA, trusted-control-plane, artifact-verification, and idempotent-recovery
principles are retained here.

## First-release acceptance

For the first release on this engine, independently prove:

- the tag resolves to the reviewed notes merge SHA;
- GitHub has one non-prerelease latest Release with complete CLI and Desktop
  assets;
- npm root and all six platform packages record that SHA and
  `latest == canary == next`;
- R2 immutable and latest manifests agree and every referenced asset exists;
- Homebrew and reasonix.io show the same version;
- old Desktop and CLI bridge clients can upgrade to the official release.

The release is incomplete until `release-ledger.json` records every surface.
