// Run: tsx src/__tests__/transcript-reader-bottom-hold.test.ts

import {
  INITIAL_TRANSCRIPT_SCROLL_STATE,
  reduceTranscriptScroll,
  type TranscriptScrollEvent,
  type TranscriptScrollState,
} from "../lib/transcriptScrollArbiter";
import {
  createTranscriptReaderBottomHold,
  MAX_TAIL_MOUNT_CHECKS,
} from "../lib/transcriptReaderBottomHold";

let passed = 0;
let failed = 0;
const check = (condition: unknown, label: string) => {
  if (condition) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
};

console.log("\ntranscript reader bottom hold");

let nextFrame = 1;
const frames = new Map<number, FrameRequestCallback>();
globalThis.requestAnimationFrame = (callback) => {
  const id = nextFrame++;
  frames.set(id, callback);
  return id;
};
globalThis.cancelAnimationFrame = (id) => { frames.delete(id); };

const element = {
  scrollTop: 500,
  scrollHeight: 1_000,
  clientHeight: 500,
  dataset: { transcriptRowCount: "10", transcriptFirstItemIndex: "100" },
  querySelector: () => null,
  querySelectorAll: () => [],
} as unknown as HTMLDivElement;
const stateRef = {
  current: reduceTranscriptScroll(
    { ...INITIAL_TRANSCRIPT_SCROLL_STATE, scrollable: true },
    { type: "NATIVE_SCROLLBAR_BEGIN" },
  ).state,
};
stateRef.current = reduceTranscriptScroll(stateRef.current, { type: "NATIVE_SCROLLBAR_END" }).state;
const scrollRef = { current: element };
const generationRef = { current: 7 };
const commands: string[] = [];
const dispatch = (event: TranscriptScrollEvent) => {
  const result = reduceTranscriptScroll(stateRef.current, event);
  stateRef.current = result.state;
  commands.push(...result.commands.map((command) => command.type));
};
const deliverScrollRef: { current: ((target?: HTMLDivElement) => void) | null } = { current: null };
const [, deliver] = createTranscriptReaderBottomHold({
  scrollRef,
  stateRef: stateRef as { current: TranscriptScrollState },
  generationRef,
  deliverScrollRef,
  dispatch,
});
deliverScrollRef.current = () => deliver(element);

deliver(element);
for (let index = 0; index <= MAX_TAIL_MOUNT_CHECKS; index += 1) {
  const pending = [...frames.values()];
  frames.clear();
  pending.forEach((callback) => callback(index));
}

check(stateRef.current.mode === "tail-follow", "an acknowledged native bottom cannot remain manual when LAST mount stalls");
check(commands.filter((command) => command === "SCROLL_TO_LAST").length === 1,
  "a stalled LAST mount hands off to exactly one arbiter-owned tail transaction");

// A mounted tail whose native extent changes every frame must spend the same
// total budget. Otherwise each changed extent resets the two-sample hold and
// a released scrollbar thumb can remain manual forever.
let unstableExtent = 1_000;
const tailRow = { dataset: { itemIndex: "109" } };
const unstableElement = {
  get scrollTop() { return unstableExtent - 500; },
  get scrollHeight() { return unstableExtent; },
  clientHeight: 500,
  dataset: { transcriptRowCount: "10", transcriptFirstItemIndex: "100" },
  querySelector: () => null,
  querySelectorAll: () => [tailRow],
} as unknown as HTMLDivElement;
const unstableStateRef = {
  current: reduceTranscriptScroll(
    { ...INITIAL_TRANSCRIPT_SCROLL_STATE, scrollable: true },
    { type: "NATIVE_SCROLLBAR_BEGIN" },
  ).state,
};
unstableStateRef.current = reduceTranscriptScroll(unstableStateRef.current, { type: "NATIVE_SCROLLBAR_END" }).state;
const unstableCommands: string[] = [];
const unstableDispatch = (event: TranscriptScrollEvent) => {
  const result = reduceTranscriptScroll(unstableStateRef.current, event);
  unstableStateRef.current = result.state;
  unstableCommands.push(...result.commands.map((command) => command.type));
};
const unstableDeliverRef: { current: ((target?: HTMLDivElement) => void) | null } = { current: null };
const [, deliverUnstable] = createTranscriptReaderBottomHold({
  scrollRef: { current: unstableElement },
  stateRef: unstableStateRef as { current: TranscriptScrollState },
  generationRef,
  deliverScrollRef: unstableDeliverRef,
  dispatch: unstableDispatch,
});
unstableDeliverRef.current = () => deliverUnstable(unstableElement);
deliverUnstable(unstableElement);
for (let index = 0; index <= MAX_TAIL_MOUNT_CHECKS; index += 1) {
  unstableExtent += 2;
  const pending = [...frames.values()];
  frames.clear();
  pending.forEach((callback) => callback(index));
}
check(unstableStateRef.current.mode === "tail-follow", "a perpetually revising mounted tail cannot strand native-thumb release");
check(unstableCommands.filter((command) => command === "SCROLL_TO_LAST").length === 1,
  "the unstable mounted tail also hands off to one bounded jump-tail transaction");

if (failed > 0) {
  console.error(`\n${failed} transcript reader bottom hold test(s) failed; ${passed} passed.`);
  process.exit(1);
}
console.log(`\n${passed} transcript reader bottom hold tests passed.`);
