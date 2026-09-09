# OpenCode Go v10 release acceptance

This gates the next stable candidate; it does not publish or approve a release.
See the [English guide](OPENCODE_GO_UPGRADE.md) and [中文说明](OPENCODE_GO_UPGRADE.zh-CN.md).
Run from the repository root unless specified otherwise.

| Gate | Verification |
| --- | --- |
| Legacy Flash/Pro/Vision, protocol/effort contract, route/account/custom migration | `go test ./internal/boot ./internal/config ./internal/provider/... -run OpenCodeGo -count=1` |
| Execution disabled with planner max; failed planner preserves runtime/history | In `desktop/`: `go test . -run 'OpenCodeGoUpgradeEffort\|ReloadRuntimeRetriesFailed' -count=1` |
| Concurrent edits, interrupted config/journal commits, retry and aliases | `go test -race ./internal/config ./internal/boot -run 'OpenCodeGo\|RoleReasoning\|BuildRetains' -count=1` |
| Actual 1.38.2 reader/save/re-upgrade | Set `REASONIX_V1382_CHECKOUT` to a separate checkout of `f5745bae24a56578e68a81f13e89f09961782d33`; run `go test ./internal/config -run '^TestOpenCodeGoV10OldReaderRoundTrip$' -v -count=1` |
| Full Go suites | `go test ./...` in root and separately in `desktop/` |
| Desktop race checks | In `desktop/`: `go test -race . -run 'OpenCodeGoUpgradeEffort\|ReloadRuntimeRetriesFailed' -count=1` |
| Frontend build/static/CSS/budgets | `pnpm --dir desktop/frontend build` |
| Transcript/navigation lifecycle | `pnpm --dir desktop/frontend test:transcript` and `pnpm --dir desktop/frontend test:app-lifecycle` |
| Real browser model save, effort switch, failed startup/settings/retry | Start Vite at `127.0.0.1:5279`, then `node desktop/frontend/bench/opencode-go-upgrade.mjs`; optional `REASONIX_OPENCODE_BROWSER_URL` |
| Windows native | Execute the Desktop test binary for `OpenCodeGoUpgradeEffortSwitchPreservesPlannerAndHistory` and `ReloadRuntimeRetriesFailedNewSession`, and exercise candidate WebView2 UI with an isolated `REASONIX_HOME` |
| Actual APIs, tool reasoning replay, image, independent search | With process-local `OPENCODE_GO_API_KEY`: `go test -tags live ./internal/boot -run '^TestLiveOpenCodeGoV10Acceptance$' -v -count=1` |

The opt-in API harness sends synthetic prompts, an in-memory image, and a fixed
tool result, without user history. A complete run makes 11 logical requests,
requesting 128–512 output tokens each. Search can account for additional internal
tokens. Record HTTP attempts and actual prompt/completion usage, including
failed probes. Textual URLs alone do not prove native search execution.

Browser fixtures exercise real DOM against a controlled bridge; they complement
native Go/Wails checks. Windows cross-compilation alone does not satisfy native
execution. Focused API reruns after fixture corrections are valid evidence when
the record identifies every case and counts failed requests.

Implementation probes on 2026-09-08 covered all three DeepSeek Chat models at
`max`, MiniMax M3 Anthropic, Grok 4.6 Responses, three-turn tool replay, actual
image recognition, and structured native search sources through both search
adapters. Supported alternate Qwen3.8 Max Anthropic also succeeded. Initial image
and search fixtures failed; corrected focused reruns passed. The 15 logical
requests totaled 23,023 prompt and 1,615 reported completion tokens. The final
four requests had independent HTTP counters and each used one attempt. Rerun
all gates on the final release candidate.

Windows implementation validation used a running Windows 11 guest
(`10.0.26200.9168`, amd64 test binary) and a separate `REASONIX_HOME` with fixture
credentials. Native controller tests passed for all three models. The actual
WebView2/Wails application also built fresh sessions at `max`, switched the
composer from `auto` to `disabled` with a `max` planner, retained runtime epoch
and session path after an invalid planner rejection, and recovered a failed
startup through the visible settings/retry buttons. A temporary build overlay
enabled a loopback CDP port for UI inspection; it is not a product change.
Fresh native startup also verified that the upgrade notice appears after the
renderer heartbeat, including when controller construction finishes first.

The browser fixture reuses Vite's exact loaded bridge module and waits for the
workspace to remount before injecting its synthetic failure. Build the frontend before compiling Desktop Go tests, since
the Go embed directive reads `frontend/dist` while Vite replaces those files.
