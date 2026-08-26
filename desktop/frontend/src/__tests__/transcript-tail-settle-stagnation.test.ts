// Run: tsx src/__tests__/transcript-tail-settle-stagnation.test.ts

import assert from "node:assert/strict";
import { createTranscriptTailSettle } from "../lib/transcriptTailSettle";
import type { TranscriptScrollWriteRecord } from "../lib/transcriptScrollProbe";

console.log("\ntranscript tail settle stagnant resend bound");

(globalThis as Record<string, unknown>).window = {
  setTimeout: (handler: TimerHandler, timeout?: number) => setTimeout(handler as () => void, timeout),
  clearTimeout: (id: number | undefined) => clearTimeout(id),
};
const frameQueue: Array<FrameRequestCallback> = [];
(globalThis as Record<string, unknown>).requestAnimationFrame = (callback: FrameRequestCallback) => {
  frameQueue.push(callback);
  return frameQueue.length;
};
(globalThis as Record<string, unknown>).cancelAnimationFrame = () => {};

type EngineRejects = boolean;

function runFixture(engineRejects: EngineRejects) {
  const element = { scrollTop: 863, scrollHeight: 1_000, clientHeight: 100 };
  const writes: TranscriptScrollWriteRecord[] = [];
  const layoutTransient = { current: false };
  const settle = createTranscriptTailSettle({
    writer: {
      write: (request) => {
        writes.push({ ...(request as unknown as TranscriptScrollWriteRecord), owner: "tail-follow", kind: request.operation });
        // An engine clamp/consume that leaves the physical offset unchanged
        // reproduces the doomed-resend regime.
        if (!engineRejects) element.scrollTop = request.top ?? element.scrollTop;
        return true;
      },
      lastOwner: () => "tail-follow",
    },
    scrollRef: { current: element as unknown as HTMLDivElement },
    modeRef: { current: "tail-follow" as const },
    generationRef: { current: 7 },
    layoutTransientRef: layoutTransient,
    requestResidualGeometry: () => {},
  });

  const pumpRevision = () => {
    settle.schedule(9_000 + writes.length, false, "geometry-changed");
    while (frameQueue.length > 0) {
      const batch = frameQueue.splice(0);
      for (const callback of batch) callback(0);
    }
    // Settle writes land synchronously on the first tick; residual
    // verification arms real timers that this fixture intentionally ignores.
  };
  return { element, writes, settle, pumpRevision };
}

{
  const { writes, pumpRevision } = runFixture(true);
  for (let index = 0; index < 8; index += 1) pumpRevision();
  assert.ok(writes.length === 3,
    `identical rejected resends stay bounded at three total writes (got ${writes.length as number})`);
  console.log("  PASS  identical rejected resends stay bounded at three total writes");
}

{
  const { element, writes, pumpRevision } = runFixture(true);
  for (let index = 0; index < 8; index += 1) pumpRevision();
  assert.ok(writes.length === 3);
  element.scrollHeight += 30;
  pumpRevision();
  const rearmCount: number = writes.length;
  assert.ok(rearmCount === 4, `re-arm spends exactly one new write (got ${rearmCount})`);
  assert.ok(writes[rearmCount - 1]?.top === element.scrollHeight - element.clientHeight,
    "the re-armed write targets the grown extent");
  console.log("  PASS  a real extent change re-arms corrections past the stagnation bound");

  for (let index = 0; index < 8; index += 1) pumpRevision();
  const resumeCount: number = writes.length;
  assert.ok(resumeCount === 6,
    `the bounded budget resumes counting after re-arm (got ${resumeCount})`);
  console.log("  PASS  the bounded budget resumes counting after re-arm");
}

{
  const { element, writes, pumpRevision } = runFixture(true);
  pumpRevision();
  pumpRevision();
  pumpRevision();
  assert.ok(writes.length === 3);
  element.scrollTop = 900;
  pumpRevision();
  assert.ok(writes.length === 3,
    "corrections a user gesture already satisfied spend no extra write");
  console.log("  PASS  corrections a user gesture already satisfied spend no extra write");
}

{
  const { writes, pumpRevision } = runFixture(false);
  for (let index = 0; index < 8; index += 1) pumpRevision();
  assert.ok(writes.length <= 2,
    `an honoring engine converges without repeat resends (got ${writes.length})`);
  console.log("  PASS  an honoring engine converges without repeat resends");
}

console.log("transcript tail settle stagnation tests passed");
