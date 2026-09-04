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

Up to 100 completed turns use full DOM. At 101 turns the adapter windows cold completed history with `@tanstack/react-virtual`; the active turn and at least the two most recent completed turns remain ordinary DOM. A former resident turn is eligible for the cold window only after it is at least one viewport above view and contains no logical anchor, selection endpoint, or focused element. Every contiguous eligible prefix is measured and published as one ledger snapshot before React transfers it out of ordinary flow, so resident-to-cold movement preserves the native extent. TanStack supplies prefix sizes and mounted ranges only: stable `getItemKey` is mandatory, automatic size-change scroll correction is disabled, and its scroll callback performs no native write.

The Window Adapter applies a range commit protocol instead of painting every asynchronous TanStack candidate. A committed range must cover the current native viewport. Native viewport geometry is consumed as an immutable external-store snapshot, allowing React to reject a concurrent render if the compositor offset advances before commit. The mounted items, total window extent, and scroll margin form one immutable adapter snapshot: retaining an old range while publishing a new extent is forbidden because that mixes measurement generations and can move or uncover content at an unchanged native `scrollTop`. Window items are positioned with absolute layout `top`, not transforms, so the item range and native scroll position cannot be split into independently committed WebView compositor transactions. A stale candidate therefore cannot replace a previously covering range; a native jump that invalidates both ranges is reconstructed synchronously from TanStack's prefix-size ledger, including every protected anchor, selection, focus, and jump block. While native input owns an unchanged viewport, measurement-only notifications retain the complete painted geometry snapshot. The adapter records whether the range came from a candidate, retention, or reconstruction, but none of these paths may write scroll position.

DOM measurement uses the same commit boundary. The adapter owns an immutable, block-keyed Reasonix measurement ledger; TanStack's item ResizeObserver path is not connected. Measurements enter a staging ledger first. In reader intent, only the anchor block and blocks after it may publish because those sizes cannot change the anchor's prefix position; measurements before the anchor remain staged until the reader reaches them. Tail intent does not refine invisible cold history: its physical geometry comes from the exact resident tail, avoiding an unrelated prefix rebuild and extra tail write. Each publish is one ledger snapshot followed by exactly one TanStack `measure()`, so correctness is independent of native wheel-event timing and a delayed wheel batch cannot cancel a correction after unsafe prefix geometry has already changed. The full-DOM adapter follows the same will-change/commit handshake. This keeps rendering, prefix sums, and native scroll ownership on one ordered state transition instead of allowing asynchronous measurements or partially updated item sizes to move visible content behind the kernel.

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

`TranscriptViewportWriter` is the only production module that may assign the native Transcript `scrollTop`. Question navigation, history prepend, Markdown block-window compensation, selection edge scrolling, the Creation scrollbar, and nested-scroll handoff all route through the kernel and writer. A request that has already landed commits with a `no-op` terminal write outcome and performs no DOM assignment. The static `check:scroll-writer` gate rejects bypasses, while runtime diagnostics record only session identity, generation, transaction, owner, intent, geometry revision, numeric offsets, and terminal outcome—never message content.

Viewport actions that can be activated during a geometry commit keep stable DOM identity. Their visibility changes on the mounted host instead of conditionally unmounting it, so a pointer or native automation target acquired before a React commit cannot become a detached no-op. The action still delegates every physical scroll to the Kernel and single writer.

Wheel, touch, scrolling keys, pointer selection, and native scrollbar drag immediately take reader ownership and cancel lower-priority work. Native thumb drag freezes program writes but never browser scrolling. The native gesture lease and post-gesture paint callbacks use the Kernel's injectable clock and are invalidated on surface-generation replacement. A physical writer offset remains pending until its matching native `scroll` event is consumed or a different offset proves real user movement, even when gesture ownership has already begun. Only native-owned scroll events update the gesture's logical anchor, and top-edge pagination additionally requires upward movement; measurement-only layout changes, delayed writer events, movement away from the history boundary, and gesture completion cannot invent a new reader position or history request. When native ownership ends, deferred structural work may resume from that observed anchor. Reduced motion affects decorative animation only.

## Geometry and safe mode

Streaming active-block ResizeObserver reports are coalesced by the kernel to at most one tail write per animation frame. Reader intent receives no tail write. Prepend and display changes restore the same logical block offset after the new projection is measured. Composer resize preserves the reader's native top and performs one tail correction only when tail owns the viewport.

Two consecutive blank-viewport, invalid-geometry, or unrecoverable-anchor events without an intervening healthy frame in one generation switch that session to full DOM until the next surface generation. Safe mode mounts only the pages currently resident in `TranscriptStore`; unloaded history and large Markdown bodies remain lazy. It reuses the same projection, components, selection model, and writer—there is no legacy renderer fallback.

## Required verification

Changes to this path must keep deterministic Kernel sequences, 100/101 rendering boundaries, active/resident ownership, stable prepend identity, stale-generation zero-write behavior, Markdown parity, selection retention, and browser/native platform replays green. Production must contain one native Transcript write point and no alternate scrolling controller.
