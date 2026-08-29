# use_capability replay eval

This is the 1.33.0 paired-run procedure for cache-stable `use_capability` versus a
prefix that expands MCP tools into the provider-visible schema. Live model runs
are optional and must use disposable Reasonix homes.

## What to measure

For the same five tasks, run each task twice:

1. **Proxy (default):** `use_capability` only. Shared Host + disk schema cache.
2. **Baseline:** a throwaway config that still expands MCP tools into the
   provider request (native Tool Search must stay off).

Record `tools/list` count, first-token latency, and cache-hit tokens. Do not
upload prompts, secrets, or workspace paths.

## Procedure

1. Use a throwaway `REASONIX_HOME` / `REASONIX_CACHE_HOME`.
2. Pick five representative tasks that need MCP discovery then a call.
3. For each task, run proxy then baseline. Keep model, effort, and workspace
   identical.
4. Write the five pairs as JSON matching
   `internal/eval/replay/testdata/paired_runs.json`.
5. Compute medians:

```bash
go test ./internal/eval/replay/ -run TestMedianReportFivePairedRuns
```

The fixture proves the median helper. Replace its numbers with live pairs when
credentials are available. Report the median `tools/list` delta and latency
delta; a proxy win is a negative list delta (fewer remote lists).

Native first-party Tool Search stays default-off and is not part of this eval.
