# Windows 10 LTSC 2019 crash investigation runbook

<a href="./WINDOWS_LTSC_2019_CRASH_RUNBOOK.zh-CN.md">简体中文</a>

This runbook is the release and evidence checklist for Windows build `17763`.
The issue remains open until the evidence gate below is satisfied; shipping the
diagnostic build alone is not a resolution.

## Release order

1. Freeze one candidate commit from `fix/windows-ltsc-crash-diagnostics` and
   record its full SHA in the experiment worksheet.
2. Back up the `reasonix-crash` D1 database.
3. Apply `workers/crash-report/migrate-diagnostics-v2.sql`. Verify the
   `report_daily` and `report_installations` tables, the
   `report_installations_fingerprint_date` index, and all added columns.
4. Deploy the crash-report Worker. Smoke-test an old Report/Ping/Metrics payload
   and a diagnostics-v2 payload with `channel=test`. Confirm the latter is under
   the development fingerprint namespace and absent from stable rankings.
5. Produce the signed Windows amd64 build from the frozen SHA. Do not move or
   recreate a published tag.
6. Complete the LTSC matrix below. Only then publish the Desktop major release.
7. In Diagnostics, retain the audit trail while applying these one-time states:
   - Ignore `[go panic] safe` / `v9.9.9`; note: `unit-test upload; fixed by
     fail-closed test endpoints`.
   - Resolve `72daba81`; set `resolved_in=desktop-v1.19.3`.
   - Ignore the old `desktop.abnormal_exit` replay group; note: `legacy
     startup-state replay contamination; replaced by lifecycle v2`.

## Compatibility smoke payloads

The old payloads omit every v2 field. The new Windows payload includes the same
32-character anonymous install ID already used by startup ping, `osBuild`,
`osRevision`, and the bounded `webview2` object. Test reports must use
`channel=test`. Never use a production-looking stable version for a smoke.

After smoke, verify:

- no raw install ID occurs in rendered HTML, audit entries, logs, or exported
  samples;
- the report sample contains only the WebView2 module basename, never its full
  path;
- identified event count and installation event count both increment on repeat;
- deleting a test group also deletes its `report_daily` and
  `report_installations` rows;
- 30-day retention removes both aggregation tables in bounded chunks.

## LTSC comparison matrix

Use the same signed candidate SHA in every cell. Record OS revision, WebView2
Runtime, GPU model, and driver version locally in the lab worksheet; GPU driver
details are not collected automatically.

| System | Runtime | GPU | Install path | Required workload |
| --- | --- | --- | --- | --- |
| Windows 10 LTSC 2019 `17763` VM | user + latest Evergreen | on/off | clean + v1.18/v1.19 upgrade | 20 cold start/exits, 10 updater restarts, 60-minute workload, 50 minimize/restores |
| Windows 10 LTSC 2019 physical GPU device | user + latest Evergreen | on/off | clean + upgrade where possible | same, plus DPI/multi-monitor and RDP connect/disconnect |
| Windows 10 22H2 `19045` | latest Evergreen | on/off | clean | same control workload |
| Windows 11 `22631` or current `26200` | latest Evergreen | on/off | clean | same control workload |

Set `REASONIX_DISABLE_WEBVIEW2_GPU=1` for the GPU-off arm and `=0` for the
GPU-on arm. Capture WER 1000/1001, Reliability Monitor time, and the matching
Diagnostics fingerprint. Dumps require explicit user authorization, a private
transfer channel, and deletion after analysis.

## Root-cause evidence gate

A root cause is confirmed only when either:

- at least two LTSC nodes reproduce while the control systems do not; or
- at least three distinct `17763` installations share one fingerprint, there
  are at least 30 active LTSC installations, and the `17763` impact rate is at
  least three times the `19045` rate.

Apply a build-17763 GPU default only if GPU-on reproduces at least `2/20` per
LTSC device while GPU-off has `0/40` across two devices and zero failures in two
hours per device. Preserve the environment-variable reverse override.

- `integrity_failure`: investigate signatures, injected DLLs, policy, and
  security software; do not apply the GPU workaround.
- `out_of_memory`: investigate memory and session-resource pressure.
- Runtime clustering: add a minimum Runtime check and an in-place update prompt.
- Renderer-only failures with `reload_succeeded` are recovered WebView2 events,
  not Reasonix application crashes.
- A lifecycle-v2 abnormal exit without a native cause requires matching WER or
  an authorized dump before closure.

## Seven-day production watch

Check daily for seven complete UTC days:

- diagnostics-v2 identity coverage is at least 95%; below 90%, do not quote an
  exact impact percentage;
- no new-version sample appears under the legacy replay fingerprint;
- browser fatal, renderer recovered/recovery-failed, degraded child-process,
  and generic lifecycle-v2 totals form a coherent explanation;
- compare `17763` and `19045` affected-install rates and review new
  fingerprints;
- confirm retention and ingest sentinels remain healthy.

If production reveals a product root cause, ship a minimal patch containing
only that fix and its regression test. Require `0/40` LTSC lab reproductions,
then observe another seven days. If evidence is still insufficient after the
first window, extend observation to 30 days and keep the issue explicitly open.
