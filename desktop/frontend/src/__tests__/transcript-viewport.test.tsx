import { createTranscriptHarness } from "./transcript-dom-harness";
import type { Item } from "../lib/useController";
import { commitTranscriptWindowRange } from "../lib/transcriptWindowRange";
import { TranscriptViewportWriter } from "../lib/transcriptViewportWriter";
import { act } from "react";

let passed = 0;
let failed = 0;
function ok(condition: unknown, label: string) {
  if (condition) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}
function turns(count: number): Item[] {
  return Array.from({ length: count }, (_, index) => [
    { kind: "user", id: `user-${index}`, text: `question ${index}`, historyTurn: index + 1 } as Item,
    { kind: "assistant", id: `answer-${index}`, text: `answer ${index}`, reasoning: "", streaming: false } as Item,
  ]).flat();
}

console.log("\nTranscript viewport adapters");
const previousRange = {
  structureRevision: "stable",
  scrollTop: 100,
  scrollMargin: 0,
  totalSize: 20_000,
  items: [{ index: 0, start: 50, end: 900 }],
  source: "candidate" as const,
};
const staleCandidate = [{ index: 50, start: 5_000, end: 5_800 }];
const measurements = Array.from({ length: 200 }, (_, index) => ({ index, start: index * 100, end: (index + 1) * 100 }));
const retained = commitTranscriptWindowRange({
  candidate: staleCandidate,
  measurements,
  retainedIndexes: new Set(),
  previous: previousRange,
  structureRevision: "stable",
  scrollTop: 180,
  clientHeight: 600,
  scrollMargin: 0,
  totalSize: 20_000,
  overscan: 2,
  gestureActive: true,
});
ok(retained.items === previousRange.items, "a stale late range cannot replace native viewport coverage");
const measuredCandidate = [{ index: 0, start: 40, end: 940 }];
const measurementOnly = commitTranscriptWindowRange({
  candidate: measuredCandidate,
  measurements,
  retainedIndexes: new Set(),
  previous: previousRange,
  structureRevision: "stable",
  scrollTop: 100,
  clientHeight: 600,
  scrollMargin: 0,
  totalSize: 20_120,
  overscan: 2,
  gestureActive: true,
});
ok(measurementOnly.items === previousRange.items, "a measurement-only range commit stays frozen during native ownership");
ok(measurementOnly.totalSize === previousRange.totalSize, "a retained range keeps its matching extent snapshot");
const released = commitTranscriptWindowRange({
  candidate: measuredCandidate,
  measurements,
  retainedIndexes: new Set(),
  previous: measurementOnly,
  structureRevision: "stable",
  scrollTop: 100,
  clientHeight: 600,
  scrollMargin: 0,
  totalSize: 20_120,
  overscan: 2,
  gestureActive: false,
});
ok(released.items !== previousRange.items, "gesture release commits the latest covering measurements");
ok(released.totalSize === 20_120, "gesture release commits range and extent atomically");
const windowSource = await import("node:fs/promises").then((fs) => fs.readFile(new URL("../components/TranscriptWindow.tsx", import.meta.url), "utf8"));
ok(windowSource.includes("useCachedMeasurements: true"), "TanStack cannot publish ResizeObserver sizes outside the viewport commit protocol");
ok(windowSource.includes("measurementLedger.stage(changes)"), "DOM measurements enter the block-keyed staging ledger before publication");
ok(
  windowSource.includes("item.start >= nativeViewport.scrollTop + nativeViewport.clientHeight - 0.5")
    && windowSource.includes("domSafeIndex")
    && windowSource.includes("paintedSafeIndex == null || domSafeIndex == null")
    && windowSource.includes("canPublishTranscriptMeasurement({")
    && windowSource.includes("userGestureActive: kernel.userGestureActive")
    && windowSource.includes("measurementLedger.publishStaged(")
    && windowSource.includes("virtualizer.resizeItem(index, change.size);"),
  "reader measurements publish only after native ownership ends and both prefix and DOM place a block beyond the painted viewport",
);
ok(!windowSource.includes("virtualizer.measure();"), "a safe suffix publish cannot invalidate and rebuild the protected prefix");
ok(windowSource.includes("measurementLedger.commit(residentChanges)"), "resident blocks publish exact sizes before leaving ordinary DOM");
ok(
  windowSource.includes("useSyncExternalStore(subscribe, getSnapshot, getSnapshot)")
    && windowSource.includes("scrollTop: nativeViewport.scrollTop")
    && windowSource.includes("clientHeight: nativeViewport.clientHeight")
    && windowSource.includes("getBoundingClientRect().top - scrollElement.getBoundingClientRect().top + nativeViewport.scrollTop"),
  "range commits use a tear-free native viewport snapshot instead of mutable render-time geometry",
);
ok(
  windowSource.includes("top: virtualItem.start - committedRange.scrollMargin")
    && !windowSource.includes("transform: `translateY(${virtualItem.start - scrollMargin}px)`"),
  "window items use layout coordinates so delayed compositor layers cannot replay an old scroll offset",
);
const reconstructed = commitTranscriptWindowRange({
  candidate: staleCandidate,
  measurements,
  retainedIndexes: new Set([80]),
  structureRevision: "stable",
  scrollTop: 1_200,
  clientHeight: 600,
  scrollMargin: 0,
  totalSize: 20_000,
  overscan: 2,
  gestureActive: true,
});
ok(reconstructed.source === "reconstructed", "an uncovered native jump reconstructs from the prefix-size ledger");
ok(reconstructed.items.some((item) => item.start <= 1_200 && item.end >= 1_300), "the reconstructed range covers the native viewport");
ok(reconstructed.items.some((item) => item.index === 80), "reconstruction retains protected blocks");

const largeMeasurements = Array.from({ length: 10_000 }, (_, index) => ({ index, start: index * 96, end: (index + 1) * 96 }));
const rangeStartedAt = performance.now();
const largeRange = commitTranscriptWindowRange({
  candidate: [{ index: 2, start: 192, end: 288 }],
  measurements: largeMeasurements,
  retainedIndexes: new Set([9_999]),
  structureRevision: "10k",
  scrollTop: 720_000,
  clientHeight: 800,
  scrollMargin: 0,
  totalSize: 960_000,
  overscan: 12,
  gestureActive: true,
});
const rangeElapsedMs = performance.now() - rangeStartedAt;
ok(rangeElapsedMs < 1_000, `10,000-turn range reconstruction completes within 1s (${rangeElapsedMs.toFixed(1)}ms)`);
ok(largeRange.source === "reconstructed" && largeRange.items.length <= 40, "10,000-turn reconstruction keeps a bounded mounted range");
ok(largeRange.items.some((item) => item.start <= 720_000 && item.end >= 720_096), "10,000-turn reconstruction covers the authoritative viewport");
ok(largeRange.items.some((item) => item.index === 9_999), "10,000-turn reconstruction preserves protected block identity");
const harness = await createTranscriptHarness({ viewportHeight: 800, rowHeight: 24 });
try {
  const writerTarget = document.createElement("div");
  let writerTop = 400;
  let physicalAssignments = 0;
  Object.defineProperties(writerTarget, {
    scrollTop: {
      configurable: true,
      get: () => writerTop,
      set: (value: number) => { physicalAssignments += 1; writerTop = value; },
    },
    scrollHeight: { configurable: true, get: () => 1_000 },
    clientHeight: { configurable: true, get: () => 600 },
  });
  const writer = new TranscriptViewportWriter();
  writer.attach(writerTarget, 7);
  const noOpWrite = writer.write({
    session: "writer-no-op",
    generation: 7,
    transactionId: 1,
    geometryRevision: 1,
    owner: "tail-follow",
    intent: "tail",
    offset: Number.POSITIVE_INFINITY,
  });
  ok(noOpWrite.accepted && noOpWrite.changed === false && physicalAssignments === 0, "writer commits an already-landed tail transaction without a redundant DOM assignment");

  await harness.render(turns(100), { geometrySessionKey: "threshold-100" });
  ok(harness.container.querySelector('[data-transcript-render-mode="full"]') != null, "100 completed turns render in full-DOM mode");
  ok(harness.container.querySelectorAll("[data-transcript-block-key]").length === 100, "full-DOM mode mounts every complete turn block");

  await harness.render(turns(101), { geometrySessionKey: "threshold-101" });
  await harness.settle();
  const projection = harness.container.querySelector<HTMLElement>('[data-transcript-render-mode="windowed"]');
  const mounted = Number.parseInt(projection?.dataset.transcriptMountedBlocks ?? "999", 10);
  ok(Boolean(projection), "101 completed turns switch to the TanStack window adapter");
  ok(mounted <= 40, `800px viewport mounts at most 40 completed blocks (${mounted})`);
  ok(harness.container.querySelectorAll('[data-transcript-resident-tail="true"] [data-transcript-block-key]').length >= 2, "the two latest completed turns remain resident ordinary DOM");

  const tailAction = harness.container.querySelector<HTMLButtonElement>(".transcript__jump-bottom");
  ok(Boolean(tailAction), "the jump-to-bottom action keeps a stable DOM host while hidden at the tail");
  const transcript = harness.scrollElement();
  await act(async () => {
    transcript.scrollTop = 0;
    transcript.dispatchEvent(new Event("scroll"));
    await new Promise((resolve) => setTimeout(resolve, 30));
  });
  await harness.waitFor(() => tailAction?.hidden === false, "jump-to-bottom visibility after reader takeover");
  ok(
    harness.container.querySelector(".transcript__jump-bottom") === tailAction,
    "reader takeover changes jump-to-bottom visibility without replacing its DOM identity",
  );
  await act(async () => {
    tailAction?.click();
    await new Promise((resolve) => setTimeout(resolve, 30));
  });
  await harness.waitFor(() => tailAction?.hidden === true, "jump-to-bottom visibility after tail restore");
  ok(
    transcript.scrollHeight - transcript.scrollTop - transcript.clientHeight <= 4,
    "the identity-stable jump-to-bottom action restores native tail geometry through the kernel",
  );

  const activeItems = [...turns(101), { kind: "user", id: "active-user", text: "active question", historyTurn: 102 } as Item];
  await harness.render(activeItems, { geometrySessionKey: "active", running: true, turnStartAt: Date.now() - 1_000 });
  await harness.settle();
  const active = harness.container.querySelector('[data-transcript-block-phase="active"]');
  ok(Boolean(active), "the current streaming turn is projected as one active block");
  ok(Boolean(active?.closest('[data-transcript-resident-tail="true"]')), "the active block stays outside the windowed history size ledger");
  ok(harness.container.querySelector(".transcript__live-status") != null, "an empty active process keeps its working status reachable");
} finally {
  await harness.unmount();
  await harness.close();
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed) process.exit(1);
