# Desktop agent notes

## Transcript scroll discipline

The transcript (`frontend/src/components/Transcript.tsx`) is governed by
`TimelineProjection` and the generation-aware `TranscriptKernel`. Keep these
contracts when touching anything that can move the transcript viewport.

- **Stable identity**: one complete turn is the projection, anchor, and
  virtualization unit. Block keys come from backend entry/user identity, never
  array position. Prepend and content patches must not rename mounted blocks.
- **Generation fence**: session/surface replacement increments the kernel
  generation. Every delayed measurement, timer, animation-frame callback, and
  write request carries that generation; stale work performs zero writes.
- **Single writer**: only `frontend/src/lib/transcriptViewportWriter.ts` may
  mutate the transcript's native scroll position. Full-DOM, TanStack window,
  Markdown, selection, question navigation, prepend, composer resize, and tail
  follow all submit transactions to `TranscriptKernel`. The static gate in
  `frontend/scripts/check-single-scroll-writer.mjs` must reject any bypass.
- **Explicit terminal state**: every transaction ends committed, cancelled, or
  expired. User input and selection preempt lower-priority work; question jumps
  outrank display/prepend/restore/resize, which outrank tail follow.
- **Native geometry is authoritative**: bottom means
  `scrollHeight - scrollTop - clientHeight <= 4`. TanStack computes prefix
  sizes and mounted ranges only; its measurement compensation is disabled and
  its scroll callback must never bypass the writer.
- **Covered-range commit**: the Window Adapter may paint a TanStack candidate
  only when it covers the current native viewport. Retain the last covering
  range when a candidate is stale; if a native jump invalidates both, rebuild
  once from the prefix-size ledger while preserving every protected block.
  Measurement-only notifications cannot replace the painted range while
  native input owns an unchanged viewport.
- **Atomic measurement commit**: DOM measurements cannot mutate TanStack's
  prefix-size ledger while native input owns the viewport. After ownership
  ends, the Window Adapter commits all changed block-keyed sizes to one
  immutable Reasonix ledger snapshot, then issues exactly one TanStack
  `measure()` invalidation and settles that geometry once. Never reintroduce
  per-item `resizeItem`, TanStack-owned ResizeObserver publication, or
  platform-specific scroll compensation.
- **Resident active tail**: the active turn and at least the two newest
  completed turns stay in ordinary DOM. A resident block may enter windowed
  history only after it is a viewport away and owns no anchor, focus, or
  selection endpoint. Stream growth in reader intent performs zero writes.
- **Bounded safe mode**: two blank/invalid/correction anomalies without an
  intervening healthy frame in one generation switch that session to full DOM
  until the next surface generation. Do not add a second rendering stack or a
  persistent user flag.
- **Deterministic clocks**: new scroll logic must go through the same
  injectable clock used by `TranscriptKernel` (`requestAnimationFrame`,
  `Date.now`, timer functions). No real sleeps or hidden retry clocks.
- **Race tests are mandatory**: any scroll-behavior change ships with a
  deterministic event sequence in `frontend/src/__tests__/transcript-kernel.test.ts`
  and, when relevant, a viewport/projection case. Run `pnpm test:transcript`
  before committing transcript changes.
