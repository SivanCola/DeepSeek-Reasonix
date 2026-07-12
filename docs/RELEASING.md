# Releasing

How Reasonix ships, who can ship what, and the preview-before-stable flow.

## Branch model: trunk + tags

- **`main-v2`** is the single development line (the v2 / 1.x trunk). Every PR merges here.
- **Production is a tag, not a branch.** A release is a tagged snapshot of `main-v2`:
  `v1.4.0` (CLI), `npm-v1.4.0` (npm), `desktop-v1.4.0` (desktop).
- **`v1`** is the archived 1.0/legacy line — maintenance only.
- **Hotfix** an already-released version by branching from its tag, fixing, and tagging again.

There is no separate "production" or "develop" branch by design — the preview channel
provides the pre-release buffer instead of a long-lived branch.

## Channels

| Surface | Stable | Pre-release buffer |
|---|---|---|
| npm | `latest` (current 1.x stable) | `next` (preview/rc), legacy `canary` compatibility |
| Desktop | R2 `latest/` pointer + release gateway | R2 `preview/` pointer + release gateway proxy (legacy `canary/` also updated; never on the GitHub releases page) |

A preview build is isolated: it **never** moves `latest` / desktop `latest/`.
Testers opt in explicitly. Desktop builds carry `-X main.channel=preview`, and the
desktop Settings > Updates panel lets users switch between Stable and Preview
latest pointers at any time. Older `canary` config values and R2 pointers remain
accepted as compatibility aliases for preview.

## Who can release what

| Action | Who | Mechanism |
|---|---|---|
| **Cut a preview** | any maintainer (write access) | `workflow_dispatch`; pauses for approval on the `canary` environment (same required reviewers as `release`) |
| **Ship `next` / stable** | **esengine only** | stable publish jobs gate on the `release` environment — esengine must approve before anything goes public |

Any maintainer can start either kind of release, but both pause in the Actions UI
for environment approval before anything reaches users: stable on the `release`
environment, preview on the `canary` environment. Preview needs the same human
gate because the desktop Settings toggle exposes the preview pointer to **every
user**, not just testers who manually installed a preview build.

> Repo settings backing this: Environments → `release` and `canary` carry the
> same required reviewers. `canary` historically had none; gate it (repo admin)
> by mirroring the `release` reviewers:
>
> ```sh
> gh api repos/esengine/DeepSeek-Reasonix/environments/release \
>   --jq '{reviewers:[.protection_rules[]|select(.type=="required_reviewers")|.reviewers[]|{type:.type,id:.reviewer.id}]}' \
>   | gh api -X PUT repos/esengine/DeepSeek-Reasonix/environments/canary --input -
> ```
>
> (Optional hardening: a tag ruleset restricting `v*`/`npm-v*`/`desktop-v*`
> creation to esengine, so maintainers can't even start a stable release.)

## The release loop

1. **Develop** — PRs land on `main-v2` (branch auto-deletes on merge).
2. **Cut a preview** before the intended release (e.g. heading for `1.4.0`):
   - Desktop: Actions → **Release desktop** → `channel: preview`, `base_version: 1.4.0`
   - CLI: Actions → **Release npm** → `base_version: 1.4.0`
   - Publishes `1.4.0-preview.N` to the desktop R2 `preview/` pointer (no GitHub release) and npm pre-release channel.
3. **Test** — testers opt into Preview in desktop Settings > Updates or install
   the CLI pre-release channel, and report bugs.
4. **Fix** on `main-v2` via PRs; re-cut the preview as needed (`preview.N` bumps).
5. **Ship stable** when the preview is clean — push the three tags:
   ```sh
   git tag v1.4.0         && git push origin v1.4.0          # CLI binaries + Homebrew
   git tag npm-v1.4.0     && git push origin npm-v1.4.0      # npm -> latest
   git tag desktop-v1.4.0 && git push origin desktop-v1.4.0  # desktop -> R2 latest/
   ```
   Each stable run **waits for esengine to approve the `release` environment** before publishing.
   A stable `npm-v*` publish moves the `latest` dist-tag automatically (build.mjs)
   and release-npm.yml verifies it landed. **Do not skip the npm tag**: the stable
   CLI release (release.yml) fails when the matching `npm-v*` tag was never pushed
   — that guard exists because 1.0.0–1.17.5 shipped without stable npm tags and
   `npm update -g` silently downgraded users to 0.53.2 (#5822). A pushed tag whose
   publish is still awaiting approval only warns; release-npm.yml's verify step
   owns asserting the dist-tag lands.
6. **Next cycle** — the preview rolls on toward `1.5.0`.

## Notes

- Preview version numbers use the workflow `run_number`, so desktop and CLI
  numbers may differ (e.g. `preview.11` vs `preview.2`). Only monotonicity per channel matters.
- A stable `-rc` tag (e.g. `npm-v1.4.0-rc.1`) still ships under `next`, not stable.
- Desktop in-app updates use R2 first, then the `crash.reasonix.io` desktop release
  gateway. The gateway resolves the `desktop-v*` release line directly and never uses
  GitHub's repository-wide `/releases/latest`, because plain `v*` tags are the CLI
  release line. Stable CLI releases also carry a compatibility `latest.json` asset so
  older desktop builds that still use GitHub `latest` do not 404.
- Preview uses R2 plus the same gateway proxy for the `preview/` pointer; it never
  appears on the GitHub releases page. The legacy `canary/` pointer is updated
  alongside preview for older clients.
- Windows and Linux apply downloaded, minisign-verified artifacts in place. macOS
  applies in-app only for Developer ID signed and notarized builds; ad-hoc/local
  builds fall back to the download page.
- Signing is identical across channels except Windows Authenticode. One minisign
  key signs both channels' artifacts, and the client verifies that signature (then
  the manifest SHA-256) before anything touches disk — switching channels never
  changes the verification rules. macOS preview builds get the same Developer ID +
  notarization as stable, so channel switches preserve Gatekeeper/TCC grants.
  Windows preview installers are Authenticode-signed with the SignPath
  `test-signing` policy: manual downloads can trigger SmartScreen or be blocked by
  publisher-allowlist device policies (WDAC/AppLocker), while in-app updates are
  unaffected (the updater already verified minisign and writes the installer
  without a mark-of-the-web).
