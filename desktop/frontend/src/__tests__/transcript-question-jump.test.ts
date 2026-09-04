import { TranscriptKernel, type TranscriptKernelClock } from "../lib/transcriptKernel";

let passed = 0;
let failed = 0;
function ok(value: unknown, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}
const frames = new Map<number, FrameRequestCallback>();
let sequence = 0;
const clock: TranscriptKernelClock = {
  now: () => 0,
  requestAnimationFrame: (callback) => { const id = ++sequence; frames.set(id, callback); return id; },
  cancelAnimationFrame: (id) => { frames.delete(id); },
  setTimeout: () => ++sequence as unknown as ReturnType<typeof setTimeout>,
  clearTimeout: () => {},
};

console.log("\nTranscript question jump transaction");
const writes: number[] = [];
const kernel = new TranscriptKernel({ clock });
kernel.connectWriter((request) => { writes.push(request.offset); return { accepted: true, offset: request.offset, changed: true }; });
kernel.replaceSurface("one");
const jump = kernel.stageJumpToBlock("turn:500");
ok(jump?.status === "active", "an unmounted question starts a transaction while its block is pinned");
ok(writes.length === 0, "window mounting does not perform an estimated physical write");
kernel.advanceGeometry();
ok(Boolean(jump && kernel.correctAnchor(jump, () => 12_120)), "painted target receives its single exact logical-anchor write");
ok(jump?.status === "committed", "the question jump reaches a terminal state after paint");
const writesBeforeSwitch = writes.length;
const stale = kernel.stageJumpToBlock("turn:old");
kernel.replaceSurface("two");
kernel.advanceGeometry();
ok(stale?.status === "cancelled", "session switch cancels an old question jump");
ok(writes.length === writesBeforeSwitch, "the old generation cannot write into the replacement surface");

console.log(`\n${passed} passed, ${failed} failed`);
if (failed) process.exit(1);
