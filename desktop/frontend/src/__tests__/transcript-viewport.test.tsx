import { createTranscriptHarness } from "./transcript-dom-harness";
import type { Item } from "../lib/useController";

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
const harness = await createTranscriptHarness({ viewportHeight: 800, rowHeight: 24 });
try {
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
