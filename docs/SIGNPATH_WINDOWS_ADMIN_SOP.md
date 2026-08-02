# SignPath Windows release administration

This SOP covers the single official Reasonix release flow. Windows payload and
installer signing is an automated child of **Publish release**; maintainers do
not approve individual SignPath requests.

## Trust boundary

- Production signing is accepted only from the protected
  `.github/workflows/release-desktop.yml` control plane called by
  `.github/workflows/release-stable.yml`.
- The product checkout is the immutable candidate SHA approved by the parent
  workflow. The control-plane checkout is `github.workflow_sha`; the two are
  validated separately.
- SignPath project, organization, policy, artifact configurations, and trusted
  certificate fingerprints are repository/environment configuration, never
  candidate-controlled inputs.
- The production policy is `release-signing`. Test certificates are forbidden
  for public artifacts.

## Required GitHub configuration

The `release` environment contains one reviewer group and these secrets or
variables:

- SignPath organization ID, project slug, API token, and trusted certificate
  fingerprint;
- the signing-contract attestation used by the Desktop workflow;
- minisign keys required for cross-platform update assets;
- `RELEASE_TAGGER_APP_ID` and `RELEASE_TAGGER_PRIVATE_KEY`;
- R2 and npm publication credentials.

SignPath must allow only the protected official control-plane workflow. There
must be no environment protection on child signing jobs: the parent `release`
approval is the sole human gate.

## Release behavior

After the environment approval, **Publish release** runs a no-publication
signing preflight for x64 and ARM64. Only a successful preflight authorizes the
real Desktop build. The workflow then:

1. submits payload archives using the reviewed artifact configuration;
2. waits for SignPath completion without an external approval prompt;
3. verifies Authenticode trust, subject, fingerprint, and architecture;
4. builds and signs installers from the verified payloads;
5. minisign-signs all updater artifacts;
6. validates the manifest before upload;
7. uploads to the draft unified GitHub Release and immutable R2 directory.

The parent advances public pointers only after CLI, npm, and Desktop staging
all succeed.

## Failure and recovery

Do not move a tag, replace an immutable R2 object, or overwrite a conflicting
GitHub asset. Use **Recover release** with the existing `vX.Y.Z`. Recovery may
fill a missing signed pair only when every existing pair, manifest, and
candidate SHA validates. A mismatched certificate, signature, manifest URL, or
payload hash is a hard failure and requires investigation or a higher patch
release.

## Periodic audit

- Confirm the allowed workflow definitions and production policy have not
  broadened.
- Confirm no child workflow or SignPath policy introduces another manual gate.
- Run the signing preflight after certificate, policy, runner, or installer
  changes.
- Verify the latest public `release-ledger.json` includes GitHub, R2, npm,
  Homebrew, and website status for the exact candidate SHA.
