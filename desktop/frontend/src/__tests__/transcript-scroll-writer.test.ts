// Run: tsx src/__tests__/transcript-scroll-writer.test.ts

import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import type { RefObject } from "react";
import type { VirtuosoHandle } from "react-virtuoso";
import type { TranscriptScrollMode } from "../lib/transcriptScrollArbiter";
import type { TranscriptScrollWriteRecord } from "../lib/transcriptScrollProbe";
import { createTranscriptScrollWriter } from "../lib/transcriptScrollWriter";

console.log("\ntranscript scroll writer");

const dom = new JSDOM('<div id="scroll"></div>');
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
const element = dom.window.document.getElementById("scroll") as HTMLDivElement;
Object.defineProperties(element, {
  scrollTop: { configurable: true, writable: true, value: 320 },
  scrollHeight: { configurable: true, value: 2_000 },
  clientHeight: { configurable: true, value: 800 },
});

const calls: Array<{ operation: string; value: unknown }> = [];
const nativeScrolls: ScrollToOptions[] = [];
element.scrollTo = (value: ScrollToOptions) => {
  nativeScrolls.push(value);
  if (value.top !== undefined) element.scrollTop = value.top;
};
const writes: TranscriptScrollWriteRecord[] = [];
dom.window.__REASONIX_TRANSCRIPT_SCROLL_WRITE__ = (write) => writes.push(write);
const handle = {
  scrollTo: (value: { top?: number }) => {
    calls.push({ operation: "scrollTo", value });
    if (value.top !== undefined) element.scrollTop = value.top;
  },
  scrollBy: (value: { top?: number }) => {
    calls.push({ operation: "scrollBy", value });
    if (value.top !== undefined) element.scrollTop += value.top;
  },
  scrollToIndex: (value: unknown) => calls.push({ operation: "scrollToIndex", value }),
} as unknown as VirtuosoHandle;
const virtuosoRef = { current: handle } as RefObject<VirtuosoHandle | null>;
const scrollRef = { current: element } as RefObject<HTMLDivElement | null>;
const modeRef = { current: "tail-follow" as TranscriptScrollMode } as RefObject<TranscriptScrollMode>;
const generationRef = { current: 4 } as RefObject<number>;
const writer = createTranscriptScrollWriter({ virtuosoRef, scrollRef, modeRef, generationRef });

assert.equal(writer.write({
  owner: "tail-follow",
  operation: "scrollTo",
  top: 1_200,
  behavior: "auto",
  source: "geometry-changed",
  expectedGeneration: 4,
  geometryRevision: 9,
}), true);
assert.equal(calls[0]?.operation, "scrollTo", "auto pixel writes synchronize Virtuoso's native scroll callback");
assert.equal(nativeScrolls.length, 1, "ordinary absolute writes also synchronize the current native scroller");
assert.equal(element.scrollTop, 1_200);
assert.deepEqual(
  { sequence: writes[0]?.sequence, generation: writes[0]?.generation, revision: writes[0]?.geometryRevision, owner: writes[0]?.owner },
  { sequence: 1, generation: 4, revision: 9, owner: "tail-follow" },
  "accepted writes carry ownership, sequence, generation, and revision",
);

generationRef.current = 5;
assert.equal(writer.write({
  owner: "recovery",
  operation: "scrollBy",
  top: 400,
  source: "recovery-end",
  expectedGeneration: 4,
  geometryRevision: 9,
}), false, "a stale async generation is rejected");
assert.equal(calls.length, 1);
assert.equal(writes.length, 1, "rejected writes emit no misleading diagnostic event");

modeRef.current = "native-thumb";
assert.equal(writer.write({
  owner: "jump",
  operation: "scrollToIndex",
  index: 42,
  source: "jump-index",
  expectedGeneration: 5,
  geometryRevision: 10,
}), false, "native thumb ownership blocks imperative writes");
assert.equal(calls.length, 1);

modeRef.current = "programmatic";
assert.equal(writer.write({
  owner: "jump",
  operation: "scrollToIndex",
  index: 42,
  source: "jump-index",
  expectedGeneration: 5,
  geometryRevision: 10,
}), true);
assert.equal(calls[1]?.operation, "scrollToIndex");
assert.deepEqual(calls[1]?.value, { index: 42, align: "start", behavior: "auto" });
assert.equal(writes[1]?.sequence, 2, "sequence numbers count only delivered writes");

assert.equal(writer.write({
  owner: "tail-follow",
  operation: "scrollToIndex",
  index: "LAST",
  align: "end",
  source: "jump-bottom",
  expectedGeneration: 5,
  geometryRevision: 10,
}), true);
assert.deepEqual(calls[2]?.value, { index: "LAST", align: "end", behavior: "auto" }, "the writer can mount the measured tail before native confirmation");

modeRef.current = "reader-gesture";
assert.equal(writer.write({
  owner: "reader-stability",
  operation: "scrollTo",
  top: 1_640,
  source: "layout-height-changed",
  expectedGeneration: 5,
  geometryRevision: 11,
}), true);
assert.equal(calls.length, 3, "reader correction does not enqueue a second Virtuoso range reconciliation");
assert.equal(nativeScrolls.at(-1)?.top, 1_640, "reader correction targets the currently painted native scroller");
assert.equal(element.scrollTop, 1_640);

console.log("transcript scroll writer tests passed");
