// Run: tsx src/__tests__/transcript-reader-extent-race.test.tsx

import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import type { VirtuosoHandle } from "react-virtuoso";
import type { TranscriptScrollWriteRecord } from "../lib/transcriptScrollProbe";
import { useTranscriptScrollArbiter } from "../lib/useTranscriptScrollArbiter";

let passed = 0;
let failed = 0;

function check(condition: unknown, label: string) {
  if (condition) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

console.log("\ntranscript reader extent races");

const dom = new JSDOM('<!doctype html><html><body><div id="root"></div><div id="scroll"><div class="transcript__row" data-row-key="row-a"></div></div></body></html>', {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Element = dom.window.Element;
globalThis.Node = dom.window.Node;

let nextFrame = 1;
const frames = new Map<number, FrameRequestCallback>();
const requestFrame = (callback: FrameRequestCallback) => {
  const id = nextFrame;
  nextFrame += 1;
  frames.set(id, callback);
  return id;
};
const cancelFrame = (id: number) => void frames.delete(id);
globalThis.requestAnimationFrame = requestFrame;
globalThis.cancelAnimationFrame = cancelFrame;
dom.window.requestAnimationFrame = requestFrame;
dom.window.cancelAnimationFrame = cancelFrame;

async function flushFrames() {
  const pending = [...frames.values()];
  frames.clear();
  await act(async () => pending.forEach((callback) => callback(performance.now())));
}

const scrollWrites: TranscriptScrollWriteRecord[] = [];

const rectAt = (top: number) => ({
  top,
  bottom: top + 100,
  height: 100,
  left: 0,
  right: 800,
  width: 800,
  x: 0,
  y: top,
  toJSON: () => ({}),
});
const scrollElement = dom.window.document.getElementById("scroll") as HTMLDivElement;
const rowElement = scrollElement.querySelector<HTMLElement>(".transcript__row")!;
rowElement.getBoundingClientRect = () => rectAt(20);
Object.defineProperty(scrollElement, "clientHeight", { configurable: true, value: 725 });
scrollElement.getBoundingClientRect = () => ({
  ...rectAt(0),
  bottom: scrollElement.clientHeight,
  height: scrollElement.clientHeight,
});
let scrollExtent = 15_829;
Object.defineProperty(scrollElement, "scrollHeight", { configurable: true, get: () => scrollExtent });
Object.defineProperty(scrollElement, "scrollTop", { configurable: true, writable: true, value: 14_567.47 });

let scrollByCalls = 0;
let lastScrollByTop = 0;
dom.window.__REASONIX_TRANSCRIPT_SCROLL_WRITE__ = (write) => {
  scrollWrites.push(write);
  if (write.owner === "reader-stability") {
    scrollByCalls += 1;
    lastScrollByTop = (write.top ?? 0) - write.scrollTop;
  }
};
const virtuosoHandle = {
  // Match Virtuoso's synchronous native-scroller write so a following rAF
  // observes the accepted correction instead of replaying a test-only stale
  // scrollTop value.
  scrollBy: ({ top }: { top: number }) => { scrollElement.scrollTop += top; },
  scrollTo: ({ top }: { top: number }) => { scrollElement.scrollTop = top; },
  scrollToIndex: () => {},
  getState: () => {},
} as unknown as VirtuosoHandle;

let arbiter: ReturnType<typeof useTranscriptScrollArbiter> | undefined;
function Probe() {
  arbiter = useTranscriptScrollArbiter();
  return null;
}

const root = createRoot(dom.window.document.getElementById("root")!);
await act(async () => root.render(<Probe />));
await act(async () => {
  (arbiter!.virtuosoRef as { current: VirtuosoHandle | null }).current = virtuosoHandle;
  arbiter!.scrollerRef(scrollElement);
});

// Composer wrap shrinks the in-flow viewport. A bottom-adjacent viewport stays
// tail-owned without a synchronous write; the coalesced revision observes it.
await act(async () => arbiter?.reset());
scrollExtent = 500;
scrollElement.scrollTop = 400;
Object.defineProperty(scrollElement, "clientHeight", { configurable: true, value: 100 });
await act(async () => arbiter?.followGrowingTail());
await flushFrames();
Object.defineProperty(scrollElement, "clientHeight", { configurable: true, value: 80 });
await act(async () => arbiter?.followGrowingTail());
check(scrollElement.scrollTop === 400, "footer-driven viewport shrink performs no synchronous tail write");
await act(async () => arbiter?.deliverScroll());
check(arbiter?.isAtBottom === true, "tail-follow keeps isAtBottom through a composer-wrap gap");
await flushFrames();
Object.defineProperty(scrollElement, "clientHeight", { configurable: true, value: 725 });
scrollExtent = 15_829;
scrollElement.scrollTop = 14_567.47;

// Returned Windows geometry: the native extent collapses after a downward
// wheel and rebounds while scrollTop remains clamped 1,949px too high.
await act(async () => arbiter?.deliverScroll());
await act(async () => arbiter?.releaseTailFollow());
scrollWrites.length = 0;
await act(async () => arbiter?.onWheelIntent({
  ctrlKey: false,
  deltaMode: 0,
  deltaX: 0,
  deltaY: 133.33,
  target: scrollElement,
} as React.WheelEvent<HTMLElement>));
scrollExtent = 13_344;
scrollElement.scrollTop = 12_618.67;
rowElement.getBoundingClientRect = () => rectAt(1_836);
await act(async () => arbiter?.deliverScroll());
check(arbiter?.modeRef.current === "reader-gesture",
  "a transient physical-bottom clamp cannot claim tail ownership");
await act(async () => arbiter?.finishProgrammaticScroll());
await act(async () => arbiter?.followGrowingTail());
await flushFrames();
check(scrollByCalls === 0, "the transaction waits while the native extent remains collapsed");
scrollExtent = 15_829;
await act(async () => arbiter?.followGrowingTail());
await flushFrames();
check(scrollByCalls === 1 && lastScrollByTop > 1_900,
  `the rebound restores the logical anchor exactly once (${lastScrollByTop}px)`);
check(scrollWrites.length === 1 && scrollWrites[0].owner === "reader-stability",
  "the correction is owned by reader stability rather than recovery or tail-follow");
check(arbiter?.modeRef.current === "manual", "the correction preserves manual reader ownership");

// Touch movement is incremental: the second touchmove protects only its own
// segment rather than replaying the distance from the original touchstart.
await act(async () => arbiter?.reset());
scrollExtent = 5_000;
scrollElement.scrollTop = 2_000;
await act(async () => arbiter?.deliverScroll());
await act(async () => arbiter?.releaseTailFollow());
await act(async () => arbiter?.onTouchStartIntent({
  touches: [{ clientY: 100 }],
} as unknown as React.TouchEvent<HTMLElement>));
await act(async () => arbiter?.onTouchMoveIntent({
  touches: [{ clientY: 90 }],
} as unknown as React.TouchEvent<HTMLElement>));
scrollElement.scrollTop = 2_010;
await act(async () => arbiter?.onTouchMoveIntent({
  touches: [{ clientY: 80 }],
} as unknown as React.TouchEvent<HTMLElement>));
scrollExtent = 4_000;
scrollElement.scrollTop = 1_000;
rowElement.remove();
await act(async () => arbiter?.deliverScroll());
scrollExtent = 5_000;
scrollByCalls = 0;
lastScrollByTop = 0;
await flushFrames();
check(scrollByCalls === 1 && lastScrollByTop === 1_020,
  `consecutive touch segments use incremental geometry (${lastScrollByTop}px)`);
scrollElement.append(rowElement);

// Ordinary sub-viewport measurement jitter stays browser-owned, and a higher
// priority selection cancels the still-pending transaction.
await act(async () => arbiter?.reset());
scrollExtent = 5_000;
scrollElement.scrollTop = 2_000;
rowElement.getBoundingClientRect = () => rectAt(20);
await act(async () => arbiter?.deliverScroll());
await act(async () => arbiter?.releaseTailFollow());
scrollWrites.length = 0;
scrollByCalls = 0;
await act(async () => arbiter?.onWheelIntent({
  ctrlKey: false,
  deltaMode: 0,
  deltaX: 0,
  deltaY: 133.33,
  target: scrollElement,
} as React.WheelEvent<HTMLElement>));
scrollElement.scrollTop = 1_960;
rowElement.getBoundingClientRect = () => rectAt(60);
await act(async () => arbiter?.followGrowingTail());
await flushFrames();
check(scrollByCalls === 0 && scrollWrites.length === 0,
  "sub-viewport reverse jitter never earns a correction");
await act(async () => arbiter?.setMode("selection", "test-reader-stability-preemption"));
scrollElement.scrollTop = 1_000;
rowElement.getBoundingClientRect = () => rectAt(1_060);
await act(async () => arbiter?.followGrowingTail());
await flushFrames();
check(scrollByCalls === 0 && scrollWrites.length === 0,
  "selection ownership cancels a pending reader transaction");

// WKWebView can replace the entire Virtuoso mount window in the scroll event
// that reports a large reverse jump. Correct that event synchronously: by the
// next animation frame the old logical anchor is already unmounted and the
// user has seen the bad range for one paint.
await act(async () => arbiter?.reset());
Object.defineProperty(scrollElement, "clientHeight", { configurable: true, value: 596 });
scrollExtent = 23_349;
scrollElement.scrollTop = 22_753;
rowElement.getBoundingClientRect = () => rectAt(-11);
await act(async () => arbiter?.deliverScroll());
await act(async () => arbiter?.releaseTailFollow());
await act(async () => arbiter?.onWheelIntent({
  ctrlKey: false,
  deltaMode: 0,
  deltaX: 0,
  deltaY: 24,
  target: scrollElement,
} as React.WheelEvent<HTMLElement>));
scrollWrites.length = 0;
scrollByCalls = 0;
scrollExtent = 23_269;
scrollElement.scrollTop = 21_986;
rowElement.remove();
await act(async () => arbiter?.deliverScroll());
check(scrollByCalls === 1 && lastScrollByTop === 687,
  `an unmounted-anchor reverse jump is corrected in its scroll event (${lastScrollByTop}px)`);
check(scrollWrites.length === 1 && scrollWrites[0].owner === "reader-stability",
  "the pre-paint correction still passes through the single writer");
scrollElement.append(rowElement);

// A Virtuoso range commit can move the old visible rows without emitting a
// native scroll event. The list MutationObserver calls this pre-paint reader
// observation before replacing the transaction's logical anchor.
await act(async () => arbiter?.reset());
scrollExtent = 22_834;
scrollElement.scrollTop = 15_438;
rowElement.getBoundingClientRect = () => rectAt(-16);
await act(async () => arbiter?.deliverScroll());
await act(async () => arbiter?.releaseTailFollow());
await act(async () => arbiter?.onWheelIntent({
  ctrlKey: false,
  deltaMode: 0,
  deltaX: 0,
  deltaY: 24,
  target: scrollElement,
} as React.WheelEvent<HTMLElement>));
scrollWrites.length = 0;
scrollByCalls = 0;
scrollExtent = 23_114;
rowElement.getBoundingClientRect = () => rectAt(516);
await act(async () => arbiter?.observeReaderExtent());
check(scrollByCalls === 1 && lastScrollByTop === 532,
  `a DOM-range-only visual reverse is corrected before paint (${lastScrollByTop}px)`);
check(scrollWrites.length === 1 && scrollWrites[0].owner === "reader-stability",
  "the range-mutation correction still passes through the single writer");

// A following native wheel can arrive after the replacement range mounts but
// before its mutation observer runs. Keep the still-mounted prior anchor so
// that the new range's first row cannot bless its own visual reversal.
await act(async () => arbiter?.reset());
scrollExtent = 21_716;
scrollElement.scrollTop = 4_678;
rowElement.getBoundingClientRect = () => rectAt(-1);
const oldSecondRow = dom.window.document.createElement("div");
oldSecondRow.className = "transcript__row";
oldSecondRow.dataset.rowKey = "old-second-row";
oldSecondRow.getBoundingClientRect = () => rectAt(36);
scrollElement.append(oldSecondRow);
await act(async () => arbiter?.deliverScroll());
await act(async () => arbiter?.releaseTailFollow());
await act(async () => arbiter?.onWheelIntent({
  ctrlKey: false,
  deltaMode: 0,
  deltaX: 0,
  deltaY: 24,
  target: scrollElement,
} as React.WheelEvent<HTMLElement>));
const incomingRow = dom.window.document.createElement("div");
incomingRow.className = "transcript__row";
incomingRow.dataset.rowKey = "incoming-row";
incomingRow.getBoundingClientRect = () => rectAt(-25);
const incomingSecondRow = dom.window.document.createElement("div");
incomingSecondRow.className = "transcript__row";
incomingSecondRow.dataset.rowKey = "incoming-second-row";
incomingSecondRow.getBoundingClientRect = () => rectAt(7);
scrollElement.prepend(incomingRow);
incomingRow.after(incomingSecondRow);
oldSecondRow.remove();
scrollElement.scrollTop = 4_702;
rowElement.getBoundingClientRect = () => rectAt(559);
scrollWrites.length = 0;
scrollByCalls = 0;
await act(async () => arbiter?.onWheelIntent({
  ctrlKey: false,
  deltaMode: 0,
  deltaX: 0,
  deltaY: 24,
  target: scrollElement,
} as React.WheelEvent<HTMLElement>));
await act(async () => arbiter?.observeReaderExtent());
check(scrollByCalls === 1 && lastScrollByTop === 584,
  `a wheel cannot replace the mounted pre-swap anchor before observation (${lastScrollByTop}px)`);
check(scrollWrites.length === 1 && scrollWrites[0].owner === "reader-stability",
  "the interleaved wheel/range correction keeps the single-writer contract");
incomingRow.remove();
incomingSecondRow.remove();

// WebView2 can deliver the Virtuoso range replacement after its 180ms reader
// intent idle boundary. Keep the accepted logical row alive across that
// bounded compositor delay instead of accepting the late range as a new
// position. The fake clock makes this race deterministic without sleeping.
await act(async () => arbiter?.reset());
scrollExtent = 24_592;
Object.defineProperty(scrollElement, "clientHeight", { configurable: true, value: 725 });
scrollElement.scrollTop = 19_228;
rowElement.getBoundingClientRect = () => rectAt(-28);
await act(async () => arbiter?.deliverScroll());
await act(async () => arbiter?.releaseTailFollow());
const originalDateNow = Date.now;
let fakeNow = 10_000;
Date.now = () => fakeNow;
try {
  await act(async () => arbiter?.onWheelIntent({
    ctrlKey: false,
    deltaMode: 0,
    deltaX: 0,
    deltaY: 24,
    target: scrollElement,
  } as React.WheelEvent<HTMLElement>));
  scrollWrites.length = 0;
  scrollByCalls = 0;
  fakeNow += 300;
  await flushFrames();
  fakeNow += 60;
  scrollExtent = 24_656;
  scrollElement.scrollTop = 19_368;
  rowElement.getBoundingClientRect = () => rectAt(433);
  await act(async () => arbiter?.observeReaderExtent());
  check(scrollByCalls === 1 && lastScrollByTop > 460,
    `the delayed range replacement restores the accepted logical row (${lastScrollByTop}px)`);
  check(scrollWrites.length === 1 && scrollWrites[0].owner === "reader-stability",
    "the delayed native correction still passes through the single writer");
} finally {
  Date.now = originalDateNow;
}

// WKWebView can briefly outrun Virtuoso's mounted range. The blank native
// coordinate must not become the accepted logical position, and an async
// native correction must not be reissued while its first write is pending.
await act(async () => arbiter?.reset());
scrollExtent = 20_416;
scrollElement.scrollTop = 2_413;
rowElement.getBoundingClientRect = () => rectAt(20);
await act(async () => arbiter?.deliverScroll());
await act(async () => arbiter?.releaseTailFollow());
await act(async () => arbiter?.onWheelIntent({
  ctrlKey: false,
  deltaMode: 0,
  deltaX: 0,
  deltaY: 24,
  target: scrollElement,
} as React.WheelEvent<HTMLElement>));
scrollWrites.length = 0;
scrollByCalls = 0;
scrollElement.scrollTop = 3_655;
rowElement.getBoundingClientRect = () => rectAt(900);
await act(async () => arbiter?.deliverScroll());
const replacementRow = dom.window.document.createElement("div");
replacementRow.className = "transcript__row";
replacementRow.dataset.rowKey = "row-b";
replacementRow.getBoundingClientRect = () => rectAt(20);
rowElement.replaceWith(replacementRow);
scrollElement.scrollTop = 2_756;
const originalScrollTo = scrollElement.scrollTo;
scrollElement.scrollTo = () => {};
try {
  await act(async () => arbiter?.deliverScroll());
  await act(async () => arbiter?.observeReaderExtent());
  check(scrollByCalls === 1 && lastScrollByTop === 899,
    `the mounted replacement range restores the pre-paint blank watermark once (${lastScrollByTop}px)`);
  check(scrollWrites.length === 1 && scrollWrites[0].owner === "reader-stability",
    "an unacknowledged WebKit correction is not emitted again");
} finally {
  scrollElement.scrollTo = originalScrollTo;
  replacementRow.replaceWith(rowElement);
}

// Near-bottom input uses the same reader transaction as every other logical
// position. A synthetic >96px reverse displacement must be rejected instead
// of slipping through the old near-bottom exception.
await act(async () => arbiter?.reset());
scrollExtent = 2_000;
Object.defineProperty(scrollElement, "clientHeight", { configurable: true, value: 725 });
scrollElement.scrollTop = 1_275;
rowElement.getBoundingClientRect = () => rectAt(20);
await act(async () => arbiter?.deliverScroll());
await act(async () => arbiter?.releaseTailFollow());
scrollWrites.length = 0;
scrollByCalls = 0;
await act(async () => arbiter?.onWheelIntent({
  ctrlKey: false,
  deltaMode: 0,
  deltaX: 0,
  deltaY: 120,
  target: scrollElement,
} as React.WheelEvent<HTMLElement>));
scrollElement.scrollTop = 1_100;
await act(async () => arbiter?.followGrowingTail());
await flushFrames();
check(scrollByCalls === 1 && scrollWrites.length === 1 && scrollWrites[0].owner === "reader-stability",
  "near-bottom reader transaction rejects the same >96px reverse jump");

await act(async () => root.unmount());
dom.window.close();

if (failed > 0) {
  console.error(`\n${failed} transcript reader extent race test(s) failed; ${passed} passed.`);
  process.exit(1);
}
console.log(`\n${passed} transcript reader extent race tests passed.`);
