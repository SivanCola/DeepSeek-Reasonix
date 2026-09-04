# Transcript architecture

The desktop Transcript has one projection and one scrolling authority:

```text
TranscriptStore / ControllerLiveStore
                ↓
       TimelineProjection
                ↓
        TranscriptKernel
                ↓
  Full DOM / TanStack Window Adapter
                ↓
   TranscriptViewportWriter
                ↓
       native scroll container
```

## Projection and rendering

`TimelineProjection` is pure. One complete turn is a `TimelineBlock`, keyed from stable backend entry/user identity. History prepend, stream completion, and unrelated content patches must not rename an existing block. The active turn never enters the window size ledger.

Up to 100 completed turns use full DOM. At 101 turns the adapter windows cold completed history with `@tanstack/react-virtual`; the active turn and at least the two most recent completed turns remain ordinary DOM. A former resident turn is eligible for the cold window only after it is at least one viewport above view and contains no logical anchor, selection endpoint, or focused element. TanStack supplies prefix sizes and mounted ranges only: stable `getItemKey` is mandatory, automatic size-change scroll correction is disabled, and its scroll callback performs no native write.

The Window Adapter applies a range commit protocol instead of painting every asynchronous TanStack candidate. A committed range must cover the current native viewport. A stale candidate cannot replace a previously covering range; a native jump that invalidates both ranges is reconstructed synchronously from TanStack's prefix-size ledger, including every protected anchor, selection, focus, and jump block. While native input owns an unchanged viewport, measurement-only notifications retain the painted range. The adapter records whether the range came from a candidate, retention, or reconstruction, but none of these paths may write scroll position.

DOM measurement uses the same commit boundary. The adapter owns an immutable, block-keyed Reasonix measurement ledger; TanStack's item ResizeObserver path is not connected. Native wheel, touch, selection, or scrollbar ownership defers every DOM-to-ledger change. Once that ownership ends, the adapter captures the existing logical anchor, publishes all changed mounted sizes in one ledger snapshot, and calls TanStack `measure()` exactly once so the next range observes either the complete old prefix tree or the complete new one. The full-DOM adapter follows the same will-change/commit handshake. This keeps rendering, prefix sums, and native scroll ownership on one ordered state transition instead of allowing asynchronous measurements or partially updated item sizes to move visible content behind the kernel.

Development, test, preview, and canary builds may use the non-persistent `?transcriptRenderMode=full|windowed` diagnostic override. Stable builds ignore it.

## Kernel state machine

Persistent viewport intent is either `tail` or `reader`. The logical anchor is the tail or a stable block key plus the viewport offset inside that block. Native `scrollHeight`, `scrollTop`, and `clientHeight` are the only bottom truth.

Every structural action is a generation-bound transaction:

- user input and selection
- question jump
- display change, prepend, restore, and composer resize
- tail follow

That order is also the preemption order. Every transaction terminates as committed, cancelled, or expired; the default deadline is 1000 ms. A session or surface replacement increments `generation`, so old animation frames, timers, measurements, and commands are rejected. Structural writes use `behavior: auto`, with at most one correction per geometry revision and one recomputation from the latest anchor.

`TranscriptKernel` receives an injectable clock. Correctness tests use fake animation frames and timers; real sleeps are not a correctness mechanism.

## Single writer and gestures

`TranscriptViewportWriter` is the only production module that may assign the native Transcript `scrollTop`. Question navigation, history prepend, Markdown block-window compensation, selection edge scrolling, the Creation scrollbar, and nested-scroll handoff all route through the kernel and writer. The static `check:scroll-writer` gate rejects bypasses, while runtime diagnostics record only session identity, generation, transaction, owner, intent, geometry revision, numeric offsets, and terminal outcome—never message content.

Wheel, touch, scrolling keys, pointer selection, and native scrollbar drag immediately take reader ownership and cancel lower-priority work. Native thumb drag freezes program writes but never browser scrolling. The native gesture lease and post-gesture paint callbacks use the Kernel's injectable clock and are invalidated on surface-generation replacement. Only native scroll events update the gesture's logical anchor; measurement-only layout changes and gesture completion cannot invent a new reader position. When native ownership ends, deferred structural work may resume from that observed anchor. Reduced motion affects decorative animation only.

## Geometry and safe mode

Streaming active-block ResizeObserver reports are coalesced by the kernel to at most one tail write per animation frame. Reader intent receives no tail write. Prepend and display changes restore the same logical block offset after the new projection is measured. Composer resize preserves the reader's native top and performs one tail correction only when tail owns the viewport.

Two consecutive blank-viewport, invalid-geometry, or unrecoverable-anchor events without an intervening healthy frame in one generation switch that session to full DOM until the next surface generation. Safe mode mounts only the pages currently resident in `TranscriptStore`; unloaded history and large Markdown bodies remain lazy. It reuses the same projection, components, selection model, and writer—there is no legacy renderer fallback.

## Required verification

Changes to this path must keep deterministic Kernel sequences, 100/101 rendering boundaries, active/resident ownership, stable prepend identity, stale-generation zero-write behavior, Markdown parity, selection retention, and browser/native platform replays green. Production must contain one native Transcript write point and no alternate scrolling controller.
