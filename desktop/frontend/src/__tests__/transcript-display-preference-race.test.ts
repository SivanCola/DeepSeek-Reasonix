import { JSDOM } from "jsdom";
import { canBeginDisplayPreferenceTransaction } from "../lib/useTranscriptScrollArbiter";
import { captureVisibleTranscriptLayoutAnchor } from "../lib/transcriptVirtuosoRecovery";

let passed = 0;
let failed = 0;
function check(value: boolean, label: string): void {
  if (value) {
    passed += 1;
    console.log(`  PASS  ${label}`);
  } else {
    failed += 1;
    console.log(`  FAIL  ${label}`);
  }
}

console.log("\ntranscript display preference transaction");
check(canBeginDisplayPreferenceTransaction({
  hasScroller: true, nativeScrollbarDragging: false, readerIntent: false, selectionActive: false,
}), "a settled transcript can start a display transaction");
check(!canBeginDisplayPreferenceTransaction({
  hasScroller: true, nativeScrollbarDragging: true, readerIntent: false, selectionActive: false,
}), "native scrollbar ownership blocks display correction");
check(!canBeginDisplayPreferenceTransaction({
  hasScroller: true, nativeScrollbarDragging: false, readerIntent: true, selectionActive: false,
}), "active user reader intent blocks display correction");
check(!canBeginDisplayPreferenceTransaction({
  hasScroller: true, nativeScrollbarDragging: false, readerIntent: false, selectionActive: true,
}), "selection ownership blocks display correction");

const dom = new JSDOM("<!doctype html><html><body><div class='transcript'><div class='transcript__row' data-row-key='r-1'></div></div></body></html>");
const element = dom.window.document.querySelector(".transcript") as HTMLElement;
let rowTop = 180;
Object.defineProperty(element, "getBoundingClientRect", {
  value: () => ({ top: 100, bottom: 500, left: 0, right: 800 }),
});
const row = element.querySelector<HTMLElement>(".transcript__row")!;
Object.defineProperty(row, "getBoundingClientRect", {
  configurable: true,
  value: () => ({ top: rowTop, bottom: rowTop + 40, left: 0, right: 800 }),
});
const anchor = captureVisibleTranscriptLayoutAnchor(element as HTMLDivElement);
check(anchor?.rowKey === "r-1" && anchor.offset === 80, "mode switch captures the visible row offset");
if (anchor) {
  rowTop = 176;
  const nextOffset = captureVisibleTranscriptLayoutAnchor(element as HTMLDivElement)?.offset;
  check(nextOffset === 76, "anchor measurement exposes a four-pixel geometry drift");
}

if (failed > 0) throw new Error(`${failed} display preference checks failed`);
console.log(`  ${passed} checks passed`);
