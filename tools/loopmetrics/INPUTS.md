---
owner: @esengine
backup: @SivanCola
status: active
reviewed: 2026-10-22
---

# loopmetrics inputs: from a session to the analyzer

How to feed a real session, such as a user-reported loop, to
`loopmetrics.py`. It covers what each source holds, which metrics it can
answer, and what must be checked first. Nothing here contacts anyone: a
session file is analysed only after the owner has it.

## Sources the analyzer reads

| source | where it comes from | shape |
| --- | --- | --- |
| trajectory | `reasonix run --trajectory FILE` | one JSON object per line, `{"event": {...}}` |
| session wire log | `*.wire.jsonl` next to the session file under `~/.reasonix` (Studio and `serve` write it) | one flat event per line |

## What each source can answer

| metric | trajectory | wire log |
| --- | --- | --- |
| M1 redundant tool calls | yes | yes |
| M2 reasoning repetition | per reasoning block | per message only, printed `source: message` |
| M3 redo after continue | yes, with two files | not supported: no split at the stop |
| M4 repeated short replies | yes | no value: the log has no `text` events |
| M5 cost covariates | yes | yes |

A wire log keeps only the event kinds its pane draws (the allow-list in
`internal/frontend/serve/wirelog.go`). Streamed reasoning and text arrive in
chunks and are not kept, so a wire log can never show a within-reply loop at
block level. M4 prints `-` for it: that is "not measurable", not "no repeat".

## Check the file is complete

- A wire log stops growing at 8 MiB and drops later frames; it never truncates
  earlier ones, so what is on disk is a prefix of the session.
- The sidecar `<session>.wire.meta.json` says so with `"truncated": true`. The
  analyzer prints `wire_truncated` (and lists truncated logs in a cell). `null`
  means there is no sidecar: nothing is known, which is not "complete".
- A session that resumed in a new process repeats attempt ids (`sa-1-1`).
  Usage is therefore deduplicated on the whole usage payload, not on the id.
  In the 65 local wire logs 575 of 1,039 usage events shared an id with
  another event.

## Steps

1. Keep the file local. It holds prompts, tool output and paths; do not paste
   it into an issue or commit it. The analyzer prints aggregates only.
2. Run `python3 tools/loopmetrics/loopmetrics.py run FILE --json` and read
   `wire_truncated`, `turns`, `m1.unresolved` and `m2.source` first.
3. Read M1 and M2 against `experiments/phase1/results.md`. In 60 runs on
   short read-only work no cell had a redundant call, a cross-block overlap
   above 0.314 or a within-block repeat above 0.287.
4. State what the file cannot show (the tables above) next to any number.

## What one session is

One session is an observation, not a result: a cell needs 5 runs of one
condition. A reported loop that shows nothing by these definitions is also
information. M1 cannot see a loop that varies its arguments each round, and M2
is character overlap, so a paraphrased loop is under-counted.
