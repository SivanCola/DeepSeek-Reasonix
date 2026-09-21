import assert from "node:assert/strict";
import { test } from "node:test";
import { build } from "esbuild";
import { createRequire } from "node:module";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { BrowserSurfaceManager } from "./surfaceManager.js";
import { FakeViewFactory, silentLog } from "./fakeGuestViews.js";
import { GrantRegistry } from "./grants.js";
import type { BrowserRecorder } from "./recorder.js";

// Only native allocation is faked. Exercise the production recorder state
// machine with a deterministic barrier before surface preparation completes.
const bundle = await build({ entryPoints: [resolve("src/main/browser/recorder.ts")], bundle: true, platform: "node", format: "cjs", external: ["electron"], write: false, logLevel: "silent" });
const require = createRequire(import.meta.url);
const electron = {
  BrowserWindow: class { constructor() { assert.fail("cancelled preparation allocated a renderer"); } },
  MessageChannelMain: class { port1 = { postMessage() {}, close() {} }; port2 = {}; },
  session: { fromPartition: () => ({ setDisplayMediaRequestHandler() {} }) },
};
const module = { exports: {} as { BrowserRecorder: typeof BrowserRecorder } };
new Function("require", "module", "exports", "__dirname", bundle.outputFiles[0].text)((name: string) => name === "electron" ? electron : require(name), module, module.exports, resolve("dist"));

async function fixture() {
  const directory = await mkdtemp(join(tmpdir(), "recorder-state-"));
  const views = new FakeViewFactory();
  const surfaces = new BrowserSurfaceManager({ views, contentSize: () => null, onTakeover() {}, onCrash() {}, log: silentLog });
  const tab = await surfaces.open("https://example.test", { taskId: "task", sessionId: "session", temporary: true });
  surfaces.takeover(tab.id, "user starts recording");
  let entered!: () => void;
  const preparing = new Promise<void>(resolve => { entered = resolve; });
  tab.view.prepareCapture = signal => { entered(); return new Promise((_resolve, reject) => signal.addEventListener("abort", () => reject(signal.reason), { once: true })); };
  const recorder = new module.exports.BrowserRecorder(surfaces, new GrantRegistry({ generation: () => "g" }), directory);
  const start = recorder.userRequest(tab, "start", directory).then(() => undefined, () => undefined);
  await preparing;
  return { surfaces, tab, recorder, directory, async close() { await recorder.close(); await start; surfaces.destroyAll(); await rm(directory, { recursive: true, force: true }); } };
}

test("user recording survives ordinary input but still stops on page or service loss", async () => {
  for (const reason of ["navigation", "crash", "renderer-loss"]) {
    const f = await fixture();
    try {
      f.surfaces.takeover(f.tab.id, "user keydown");
      assert.equal((await f.recorder.userRequest(f.tab, "status", f.directory))?.state, "preparing", "user input must not cancel a user-owned recording");
      const view = f.tab.view as import("./fakeGuestViews.js").FakeGuestView;
      if (reason === "navigation") view.fire().onNavigate("https://example.test/next", false);
      else if (reason === "crash") view.fire().onRenderProcessGone("crashed");
      else f.surfaces.pauseForRendererLoss("service disconnected");
      assert.equal((await f.recorder.userRequest(f.tab, "status", f.directory))?.state, "interrupted");
    } finally { await f.close(); }
  }
});

test("stop during preparation cancels instead of waiting for the recording duration", async () => {
  const f = await fixture();
  try {
    const stop = f.recorder.userRequest(f.tab, "stop", f.directory);
    assert.equal((await f.recorder.userRequest(f.tab, "status", f.directory))?.state, "cancelled");
    await stop;
  } finally { await f.close(); }
});
