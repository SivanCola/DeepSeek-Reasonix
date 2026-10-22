---
owner: @esengine
backup: @SivanCola
status: active
reviewed: 2026-10-22
---

# loopmetrics phase 1: repetition under long-session conditions

Measured with the analyzer in `tools/loopmetrics` (definitions and thresholds
pre-registered there). Raw trajectories are not committed; `results.json`
holds every cell summary and every run.

## Setup

- Workspace: `origin/studio` at `c9b435284b3bea9833c1653680ff4a406e421d27`, a
  read-only worktree, identical for every run. Binary built from that commit.
- Model: `deepseek-flash/deepseek-flash` through the existing configured
  provider (kind `openai`, thinking enabled); `--effort` not set.
- Cells: 3 arms x 4 conditions x 5 repeats. Arm order rotated each repeat.
  Every cell has n=5 and 0 failed runs. Full parameters: `manifest.json`.
- Arms: `A` default composition; `M` `--agent-composition builtin:minimal`;
  `E` `--ablate evidence`.

| condition | what one run is |
| --- | --- |
| chain | 8 scripted read-only turns, one `run -c` per turn (cold resume each turn) |
| compact | the same with `compact_ratio` 3% in a project config, to force compaction |
| interrupt | one heavy read-only task, SIGINT after 7 s, then `-c "Continue."` |
| unverif | a fresh temp module with two contradictory tests, writes allowed, 12 steps |

Run it: `LM_FIXTURE=<worktree> LM_RX=<binary> ./batch.sh`, then
`python3 cells.py`. The driver stops at CNY 8 on its own ledger.

## Results

| cell | runs | reasoning chars median [min, max] | requests median [min, max] | cost CNY median [min, max] | cross-block max Jaccard (max over runs) | within-block max (max over runs) | redundant calls (total) | M4 max run |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| A-chain | 5 | 4835 [3466, 11584] | 31 [27, 49] | 0.101 [0.077, 0.203] | 0.217 | 0.183 | 0 / 172 | 0 |
| M-chain | 5 | 7193 [5828, 8018] | 35 [29, 37] | 0.108 [0.098, 0.133] | 0.251 | 0.265 | 0 / 152 | 0 |
| E-chain | 5 | 5733 [4159, 9658] | 29 [24, 32] | 0.098 [0.090, 0.218] | 0.291 | 0.181 | 0 / 142 | 0 |
| A-compact | 5 | 6891 [4705, 11482] | 31 [28, 66] | 0.187 [0.161, 0.362] | 0.314 | 0.245 | 0 / 174 | 0 |
| M-compact | 5 | 7369 [3300, 9218] | 36 [27, 39] | 0.197 [0.071, 0.216] | 0.278 | 0.228 | 0 / 200 | 0 |
| E-compact | 5 | 5851 [3176, 13188] | 29 [24, 33] | 0.171 [0.158, 0.195] | 0.216 | 0.253 | 0 / 140 | 0 |
| A-interrupt | 5 | 5124 [4025, 7115] | 6 [5, 6] | 0.130 [0.083, 0.158] | 0.298 | 0.176 | 0 / 188 | 0 |
| M-interrupt | 5 | 11341 [7559, 12859] | 12 [8, 17] | 0.160 [0.141, 0.171] | 0.107 | 0.287 | 0 / 135 | 0 |
| E-interrupt | 5 | 6522 [2754, 7546] | 8 [6, 9] | 0.162 [0.130, 0.169] | 0.252 | 0.142 | 0 / 191 | 0 |
| A-unverif | 5 | 5629 [3381, 9065] | 8 [7, 10] | 0.038 [0.027, 0.044] | 0.045 | 0.116 | 0 / 45 | 0 |
| M-unverif | 5 | 7392 [2927, 7714] | 5 [4, 9] | 0.022 [0.013, 0.033] | 0.054 | 0.108 | 0 / 25 | 0 |
| E-unverif | 5 | 5154 [2315, 8219] | 7 [7, 8] | 0.029 [0.024, 0.039] | 0.087 | 0.150 | 0 / 38 | 0 |

## Findings

**Null result: no loop signal in any of the 12 cells.**

- M1: 0 redundant calls out of 1,602 unique calls across all cells.
- M2: 892 reasoning blocks analysed; 958 in total, and 66 under the 40-char
  minimum are excluded.
  - Largest cross-block Jaccard: 0.314, over 611 measurable blocks.
  - Largest within-block repeat: 0.287.
  - Nothing reaches the pre-registered flags or any sensitivity value (0.7,
    0.8, 0.9 cross; 0.4, 0.5, 0.6 within).
- M3: in all 15 interrupted-and-continued runs, 0 of the first 5, 10 and 20
  calls of the continued run repeat a completed call.
- M4: no repeated short reply.
- The contradictory-test task did not loop: every run ended within 4 to 10
  model requests, and the test file was left unchanged in all 15 runs.
- The implementation was left unchanged in 14 of 15 runs; the exception is
  below. The final messages that were read stated the contradiction and asked
  which test to correct; not all 15 final messages were read.

## Did the conditions happen

- interrupt: 15 of 15 first legs were interrupted by the signal; none finished first.
- compact: compaction notices per run were A `[11, 1, 10, 1, 4]`, M
  `[0, 1, 1, 1, 0]`, E `[1, 1, 1, 1, 10]`.
  - Two M runs saw no compaction, so the condition was not exercised there.
  - Runs with 10 or 11 notices re-compacted almost every turn, because 3% of
    the window is below the starting prompt size. That is the setting, not a
    natural session.
- chain: no compaction in any run (as intended, ratio 80%).
- One leg ended with exit 1 (M-interrupt, repeat 3, leg 2) on the 12-step cap.

## Observations that were not pre-registered

Reported only as numbers, not as findings, and not a statement about loops.

- In the interrupt cell arm M used more reasoning and requests.
- Reasoning characters, median: 11,341 (M), 5,124 (A), 6,522 (E).
- Requests, median: 12 (M), 6 (A), 8 (E).
- n=5 per cell, and this contrast was not replicated.
- In unverif, arm E repeat 1 made the tests pass by inspecting the call
  stack so that `Add` returns a different value under `TestFive`. The other 14
  runs did not edit the implementation. One run in five is not a contrast.
- Cost covariates are in the table; cache-hit behaviour was not analysed here.

## Not covered

- The other agent (dsh) and any non-DeepSeek `openai` provider.
- A single-process long session (the `serve` logs carry no reasoning blocks).
- `agent.host_verification = "off"`: it needs an edit to the user's global
  configuration file, which was not changed.
- Sessions longer than 8 turns, and real user sessions that reported loops.

## Limits

- The workload is short read-only questions about one repository and one
  contradictory-test task. The reported loops come from long real sessions,
  so absence here does not say they cannot happen.
- Each turn is a cold resume (`run -c`), which exercises resume and pruning but
  not a session that stays in one process.
- Spend: about CNY 7.9 including four estimate runs.
- The cells alone cost CNY 7.15 by the sum of `usage` events and CNY 6.88 by
  the driver's ledger. An interrupted leg writes no metrics file, so the
  ledger misses it.
