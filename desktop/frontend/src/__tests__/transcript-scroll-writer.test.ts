// Run: tsx src/__tests__/transcript-scroll-writer.test.ts

import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import type { RefObject } from "react";
import type { VirtuosoHandle } from "react-virtuoso";
import type { TranscriptScrollMode } from "../lib/transcriptScrollArbiter";
import type { TranscriptScrollWriteRecord } from "../lib/transcriptScrollProbe";
import {
  createTranscriptScrollWriter,
  shouldBridgeTranscriptReaderCorrection,
} from "../lib/transcriptScrollWriter";

console.log("\ntranscript scroll writer");

const dom = new JSDOM('<div id="scroll"><div class="transcript__virtual-sizer"></div></div>');
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(dom.window.navigator, "userAgent", {
  configurable: true,
  value: "Mozilla/5.0 AppleWebKit/605.1.15 Version/17.5 Safari/605.1.15",
});
assert.equal(shouldBridgeTranscriptReaderCorrection(dom.window as unknown as Window), true);
Object.defineProperty(dom.window.navigator, "userAgent", {
  configurable: true,
  value: "Mozilla/5.0 AppleWebKit/537.36 Chrome/128.0.0.0 Safari/537.36 Edg/128.0.0.0",
});
assert.equal(shouldBridgeTranscriptReaderCorrection(dom.window as unknown as Window), true);
Object.defineProperty(dom.window.navigator, "userAgent", {
  configurable: true,
  value: "Mozilla/5.0 AppleWebKit/537.36 Chrome/128.0.0.0 Safari/537.36",
});
assert.equal(shouldBridgeTranscriptReaderCorrection(dom.window as unknown as Window), false);
Object.defineProperty(dom.window.navigator, "userAgent", {
  configurable: true,
  value: "Mozilla/5.0 AppleWebKit/605.1.15 Version/17.5 Safari/605.1.15",
});
const frames: FrameRequestCallback[] = [];
dom.window.requestAnimationFrame = ((callback: FrameRequestCallback) => {
  frames.push(callback);
  return frames.length;
}) as typeof dom.window.requestAnimationFrame;
dom.window.cancelAnimationFrame = ((id: number) => {
  frames[id - 1] = () => {};
}) as typeof dom.window.cancelAnimationFrame;
const element = dom.window.document.getElementById("scroll") as HTMLDivElement;
const list = element.querySelector<HTMLElement>(".transcript__virtual-sizer")!;
element.getBoundingClientRect = () => ({
  x: 0, y: 0, top: 0, left: 0, right: 800, bottom: 800,
  width: 800, height: 800, toJSON: () => ({}),
});
Object.defineProperties(element, {
  scrollTop: { configurable: true, writable: true, value: 320 },
  scrollHeight: { configurable: true, value: 3_000 },
  clientHeight: { configurable: true, value: 800 },
});

const calls: Array<{ operation: string; value: unknown }> = [];
const nativeScrolls: ScrollToOptions[] = [];
let nativeScrollCommits = true;
let bridgeRowRawTop: number | null = null;
element.scrollTo = ((value: ScrollToOptions) => {
  nativeScrolls.push(value);
  if (nativeScrollCommits && value.top !== undefined) {
    if (bridgeRowRawTop !== null) bridgeRowRawTop -= value.top - element.scrollTop;
    element.scrollTop = value.top;
  }
}) as typeof element.scrollTo;
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
const bridgeRow = dom.window.document.createElement("div");
bridgeRow.className = "transcript__row";
bridgeRow.dataset.rowKey = "bridge-row";
bridgeRowRawTop = 500;
bridgeRow.getBoundingClientRect = () => {
  const translateParts = list.style.translate.split(" ");
  const translate = Number.parseFloat(translateParts[translateParts.length - 1] ?? "0") || 0;
  const top = (bridgeRowRawTop ?? 0) + translate;
  return {
    x: 0, y: top, top, left: 0, right: 800, bottom: top + 100,
    width: 800, height: 100, toJSON: () => ({}),
  };
};
list.append(bridgeRow);
assert.equal(writer.write({
  owner: "reader-stability",
  operation: "scrollTo",
  top: 1_640,
  source: "layout-height-changed",
  expectedGeneration: 5,
  geometryRevision: 11,
}), true);
assert.equal(calls.length, 3, "reader correction does not enqueue a second Virtuoso range reconciliation");
assert.equal(nativeScrolls.length, 1, "large reader correction waits for its visual bridge frame");
assert.equal(list.style.translate, "0 -440px", "reader correction holds the painted logical anchor before native range reconciliation");
list.style.transform = "translateY(80px)";
assert.equal(list.style.translate, "0 -440px", "Virtuoso's range transform cannot overwrite the independent reader bridge");
frames.shift()?.(0);
assert.equal(nativeScrolls[nativeScrolls.length - 1]?.top, 1_640, "reader correction targets the currently painted native scroller on the bridge frame");
assert.equal(element.scrollTop, 1_640);
assert.equal(list.style.translate, "0 0px", "native acknowledgement retains a zero-offset bridge through the next paint");
bridgeRowRawTop += 590;
bridgeRow.style.paddingTop = "1px";
await Promise.resolve();
assert.equal(list.style.translate, "0 -590px", "a late same-paint range replacement keeps the corrected row visually fixed");
frames.shift()?.(16);
await new Promise((resolve) => dom.window.setTimeout(resolve, 0));
assert.equal(list.style.translate, "", "the acknowledged bridge releases after the protected paint");
assert.equal(list.style.transform, "translateY(80px)", "bridge cleanup preserves Virtuoso's range transform");

assert.equal(writer.write({
  owner: "reader-stability",
  operation: "scrollTo",
  top: 1_800,
  source: "layout-height-changed",
  expectedGeneration: 5,
  geometryRevision: 12,
}), true);
assert.equal(list.style.translate, "0 -160px");
generationRef.current = 6;
frames.shift()?.(16);
assert.equal(nativeScrolls[nativeScrolls.length - 1]?.top, 1_640, "stale visual bridge never writes into a replacement surface");
assert.equal(list.style.translate, "", "stale visual bridge still restores the list before paint");

modeRef.current = "reader-gesture";
nativeScrollCommits = false;
assert.equal(writer.write({
  owner: "reader-stability",
  operation: "scrollTo",
  top: 1_920,
  source: "layout-height-changed",
  expectedGeneration: 6,
  geometryRevision: 13,
}), true);
assert.equal(list.style.translate, "0 -280px");
frames.shift()?.(24);
assert.equal(list.style.translate, "0 -280px", "an unacknowledged native offset keeps the bridge painted");
nativeScrollCommits = true;
frames.shift()?.(32);
assert.equal(element.scrollTop, 1_920, "the bounded bridge retries the native target");
assert.equal(list.style.translate, "0 0px", "native acknowledgement keeps the retried bridge through paint");
frames.shift()?.(48);
await new Promise((resolve) => dom.window.setTimeout(resolve, 0));
assert.equal(list.style.translate, "", "the retried bridge releases after the protected paint");

assert.equal(writer.write({
  owner: "reader-stability",
  operation: "scrollTo",
  top: 2_200,
  source: "layout-height-changed",
  expectedGeneration: 6,
  geometryRevision: 14,
}), true);
assert.equal(list.style.translate, "0 -280px");
modeRef.current = "programmatic";
assert.equal(writer.write({
  owner: "jump",
  operation: "scrollToIndex",
  index: 14,
  source: "question-navigation",
  expectedGeneration: 6,
  geometryRevision: 14,
}), true);
assert.equal(list.style.translate, "", "a new owner cancels the pending reader visual bridge");
const nativeCountAfterJump = nativeScrolls.length;
frames.shift()?.(32);
assert.equal(nativeScrolls.length, nativeCountAfterJump, "a cancelled reader bridge cannot land after a jump");

console.log("transcript scroll writer tests passed");
