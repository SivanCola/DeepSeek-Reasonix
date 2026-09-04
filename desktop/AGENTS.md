# Desktop agent notes

## Transcript scroll discipline

The transcript (`frontend/src/components/Transcript.tsx`) is governed by
`TimelineProjection` and the generation-aware `TranscriptKernel`. Keep these
contracts when touching anything that can move the transcript viewport.

- **Stable identity**: one complete turn is the projection, anchor, and
  virtualization unit. Block keys come from backend entry/user identity, never
  array position. Prepend and content patches must not rename mounted blocks.
- **Stable viewport actions**: controls that can be activated while range or
  geometry state changes keep the same DOM identity. Visibility is state on a
  mounted action host; do not conditionally remount it across viewport commits.
- **Generation fence**: session/surface replacement increments the kernel
  generation. Every delayed measurement, timer, animation-frame callback, and
  write request carries that generation; stale work performs zero writes.
- **Single writer**: only `frontend/src/lib/transcriptViewportWriter.ts` may
  mutate the transcript's native scroll position. Full-DOM, TanStack window,
  Markdown, selection, question navigation, prepend, composer resize, and tail
  follow all submit transactions to `TranscriptKernel`. The static gate in
  `frontend/scripts/check-single-scroll-writer.mjs` must reject any bypass.
- **Scroll provenance**: a physical writer offset remains pending until its
  matching native `scroll` event is consumed or a different offset proves
  user movement. Starting a gesture must not relabel a delayed writer event as
  native input, and top-edge pagination reacts only to native-owned upward
  movement, never a writer event or a reader moving away from the boundary.
- **Explicit terminal state**: every transaction ends committed, cancelled, or
  expired. User input and selection preempt lower-priority work; question jumps
  outrank display/prepend/restore/resize, which outrank tail follow.
- **Native geometry is authoritative**: bottom means
  `scrollHeight - scrollTop - clientHeight <= 4`. TanStack computes prefix
  sizes and mounted ranges only; its measurement compensation is disabled and
  its scroll callback must never bypass the writer.
- **Covered-range commit**: the Window Adapter may paint a TanStack candidate
  only when it covers the current native viewport. Retain the last covering
  geometry snapshot when a candidate is stale: mounted items, total extent,
  and scroll margin are one atomic value and must never come from different
  measurement generations. If a native jump invalidates both, rebuild once
  from the prefix-size ledger while preserving every protected block.
  Measurement-only notifications cannot replace the painted range while
  native input owns an unchanged viewport. Native viewport geometry is an
  external store: range renders must use its immutable snapshot so React
  cannot commit a range calculated before a newer compositor scroll offset.
  Window items use absolute layout `top`, not transforms that can put range
  position and native scroll state into independently committed compositor
  transactions.
- **Anchor-safe measurement commit**: DOM measurements enter a block-keyed
  staging ledger before they can change TanStack's prefix sizes. In reader
  intent, the entire painted viewport is immutable: both the pre-measurement
  prefix range and mounted DOM must place a block after the viewport before it
  becomes a publish boundary. The logical Kernel anchor may only move that
  boundary later. This prevents stale listeners, underestimated ranges, or
  lazy blocks from reflowing any content the reader can see. TanStack's
  `scrollMargin` is measured in the native scroller's coordinate space,
  including Transcript padding and any prefix. Earlier and visible sizes remain
  staged; only post-viewport overscan may publish. Tail intent does not refine
  invisible cold history; its exact geometry belongs to resident DOM.
  Publish one immutable Reasonix snapshot, then transfer only that safe suffix
  into TanStack's keyed size cache in the same browser task. Never call
  TanStack `measure()` for a measurement publish: it clears the keyed cache and
  rebuilds the protected prefix. Never base correctness on an idle timeout,
  enable TanStack-owned ResizeObserver publication, or add platform-specific
  scroll compensation.
- **Resident active tail**: the active turn and at least the two newest
  completed turns stay in ordinary DOM. A resident block may enter windowed
  history only after it is a viewport away and owns no anchor, focus, or
  selection endpoint. Measure every contiguous leaving prefix into one ledger
  snapshot before changing the resident boundary; estimated-size migration is
  forbidden. Stream growth in reader intent performs zero writes.
- **Bounded safe mode**: two blank/invalid/correction anomalies without an
  intervening healthy frame in one generation switch that session to full DOM
  until the next surface generation. Do not add a second rendering stack or a
  persistent user flag.
- **Deterministic clocks**: new scroll logic must go through the same
  injectable clock used by `TranscriptKernel` (`requestAnimationFrame`,
  `Date.now`, timer functions). No real sleeps or hidden retry clocks.
- **No redundant physical writes**: a transaction whose requested offset has
  already landed may commit as a no-op, but must not assign `scrollTop` again.
- **Race tests are mandatory**: any scroll-behavior change ships with a
  deterministic event sequence in `frontend/src/__tests__/transcript-kernel.test.ts`
  and, when relevant, a viewport/projection case. Run `pnpm test:transcript`
  before committing transcript changes.
