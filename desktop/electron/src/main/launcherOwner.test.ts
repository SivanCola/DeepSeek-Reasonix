import assert from "node:assert/strict";
import { test } from "node:test";
import { createLauncherOwner, watchLauncherOwner, watchParentProcess } from "./launcherOwner.js";

test("launcher loss is delivered once after a real ownership connection", async () => {
  const owner = await createLauncherOwner();
  let losses = 0;
  let resolveLost!: () => void;
  const lost = new Promise<void>(resolve => { resolveLost = resolve; });
  const watcher = watchLauncherOwner(owner.value, () => { losses++; resolveLost(); });
  try {
    await watcher.ready;
    owner.close();
    await lost;
    assert.equal(losses, 1);
  } finally { watcher.dispose(); owner.close(); }
});

test("direct development launches detect reparenting once without keeping the event loop alive", t => {
  t.mock.timers.enable({ apis: ["setInterval"] });
  let parent = 42, losses = 0;
  const watch = watchParentProcess(() => { losses++; }, { parentPID: 42, currentParent: () => parent, alive: () => true });
  t.mock.timers.tick(1000); assert.equal(losses,0);
  parent = 1;
  t.mock.timers.tick(1000); t.mock.timers.tick(5000);
  assert.equal(losses,1);
  watch.dispose();
});

test("direct development launches detect parent exit and respect explicit disposal", t => {
  t.mock.timers.enable({ apis: ["setInterval"] });
  let running = true, losses = 0;
  const watch = watchParentProcess(() => { losses++; }, { parentPID: 42, currentParent: () => 42, alive: () => running });
  running = false; t.mock.timers.tick(1000);
  assert.equal(losses,1); watch.dispose();
  const disposed = watchParentProcess(() => { losses++; }, { parentPID: 42, currentParent: () => 1, alive: () => false });
  disposed.dispose(); t.mock.timers.tick(1000);
  assert.equal(losses,1);
});

test("normal shell disposal does not request a second quit", async () => {
  const owner = await createLauncherOwner();
  let losses = 0;
  const watcher = watchLauncherOwner(owner.value, () => { losses++; });
  await watcher.ready;
  watcher.dispose(); owner.close();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(losses, 0);
});

test("a stale or forged owner cannot leave an unmanaged development shell", async () => {
  const owner = await createLauncherOwner();
  let lost!: () => void;
  const loss = new Promise<void>(resolve => { lost = resolve; });
  const watcher = watchLauncherOwner(owner.value.replace(/:[^:]+$/, ":"+"a".repeat(48)), lost);
  try { await loss; await assert.rejects(watcher.ready); }
  finally { watcher.dispose(); owner.close(); }
});
