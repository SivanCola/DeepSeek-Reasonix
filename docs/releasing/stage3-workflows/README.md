# Stage 3 activation workflow drafts

These files are **not** active GitHub Actions workflows. They are the activation
diff prepared for Stage 3 and must not live under `.github/workflows/` until:

1. Stage 1 infrastructure has merged
2. `DEFAULT_MIN_PROMOTION_CANDIDATE_SHA` is set to that merge SHA
3. The `reasonix-release-tagger` App, environment secrets, and compatible tag
   rulesets are configured
4. A Preview published from the post-cutoff `main-v2` history has soaked ≥24h
5. Shadow validation has succeeded

## Contents

| Draft | Role after activation |
| --- | --- |
| `release-preview.yml` | One-input Preview orchestrator + App tag creation |
| `release-stable.yml` | Zero-input Stable promotion + App atomic tags |
| `release-preview-recovery.yml` | Isolated Preview recovery request |
| `release-stable-recovery.yml` | Isolated Stable recovery request |
| `release-stable-emergency.yml` | P0 observation-period override request |
| `release-cli-trigger.yml` | Block human Preview tags; accept App only |
| `release-stable-trigger.yml` | Block human Stable tags; accept App only |

## Activation steps

1. Copy these files over `.github/workflows/` counterparts (and add the three
   request workflows).
2. In Stage-3 promote paths set `RELEASE_REQUIRE_PROMOTION_CUTOFF=true` (or keep
   a non-empty `DEFAULT_MIN_PROMOTION_CANDIDATE_SHA`).
3. Switch repository tag rulesets to App-only create for Preview/Stable in the
   same change window.
4. Update `docs/RELEASING.md` and the `reasonix-develop` skill only after the
   workflows are live.
