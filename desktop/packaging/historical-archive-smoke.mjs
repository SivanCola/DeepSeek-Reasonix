// Exercise the unmodified production package with disposable historical data.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { packagedSmokeEnv } from "./smoke-env.mjs";
import { closeAndVerify } from "./smoke-lifecycle.mjs";
import { parseServiceReady, waitForSmokeCondition } from "./smoke-poll.mjs";

const require = createRequire(new URL("../electron/package.json", import.meta.url));
const { _electron: electron } = require("playwright");
const [executable, fixture, output] = process.argv.slice(2);
if (!output) throw new Error("usage: historical-archive-smoke.mjs <packaged executable> <fixture builder> <evidence directory>");
const evidence = resolve(output);
mkdirSync(evidence, { recursive: true });
const home = mkdtempSync(join(tmpdir(), "reasonix-archive-"));
const env = packagedSmokeEnv(process.env, home);
execFileSync(resolve(fixture), [home], { stdio: "inherit", env });
const ids = ["qa-cold-history", "qa-adopted-history"];
const originals = ids.flatMap(id => ["manifest.json", "events.frames"].map(name => {
  const file = join(home, "sessions-v4", id, name);
  return [file, readFileSync(file)];
}));
const checks = [];
let application, page, pids, version, build;
const rpc = (method, ...args) => page.evaluate(({ method, args }) => window.reasonixDesktop.invoke(method, args), { method, args });
async function launch() {
  application = await electron.launch({ executablePath: resolve(executable), env, timeout: 60000 });
  await waitForSmokeCondition(async () => {
    for (const candidate of application.windows()) {
      try {
        const value = await candidate.evaluate(() => window.reasonixDesktop.invoke("Version", []));
        page = candidate; version = value; return true;
      } catch { /* The boot window is replaced when the service becomes ready. */ }
    }
    return false;
  }, { timeout: 60000 });
  const identity = await application.evaluate(({ app }) => ({ packaged: app.isPackaged, resources: process.resourcesPath, pid: process.pid }));
  assert.equal(identity.packaged, true);
  assert.notEqual(version, "dev");
  build = JSON.parse(readFileSync(join(identity.resources, "build.json"), "utf8"));
  assert.equal(version, build.version);
  if (process.env.QA_SOURCE_SHA) assert.equal(build.commit, process.env.QA_SOURCE_SHA);
  const ready = parseServiceReady(readFileSync(join(home, "desktop-shell", "logs", "shell.log"), "utf8").split("\n").filter(line => line.includes("desktop service ready:")).at(-1) || "");
  assert.ok(ready);
  pids = { shellPid: identity.pid, servicePid: ready.pid };
  await page.addLocatorHandler(page.locator(".management-screen__back:visible"), async back => { await back.click(); });
  await page.locator(".app").waitFor({ state: "visible", timeout: 60000 });
}
async function close() {
  await closeAndVerify(application, pids);
  application = undefined;
}
async function rows() {
  return (await rpc("ListProjectTopics", { scope: "global", limit: 50 })).items;
}
async function archiveRow(label, ref) {
  const folder = page.locator(".project-tree__folder--global .project-tree__folder-main");
  await folder.waitFor({ state: "visible", timeout: 60000 });
  if (await folder.getAttribute("aria-expanded") === "false") await folder.click();
  const row = page.locator(".project-tree__topic").filter({ hasText: label }).first();
  await row.waitFor({ state: "visible", timeout: 30000 });
  await row.hover();
  await row.locator(".project-tree__topic-action--archive").click();
  await waitForSmokeCondition(async () => (await rpc("ListTrashEntries", "", "", 50)).items.some(item => item.ref?.sessionId === ref.sessionId));
  await row.waitFor({ state: "hidden", timeout: 30000 });
}
try {
  await launch();
  const historical = await rpc("ListHistoricalSessions");
  assert.equal(historical.items.length, 2);
  const adopted = historical.items.find(item => item.title === ids[1]);
  assert.ok(adopted?.source);
  const imported = await rpc("ImportHistoricalSession", adopted.id);
  await rpc("RenameSessionTarget", { ref: imported.session }, "QA previously migrated");
  await close();

  // Model a receipt from the older path-identity implementation, while the
  // original source and imported conversation remain unchanged.
  const registryPath = join(home, "desktop", "workspace-state-v1.json");
  const state = JSON.parse(readFileSync(registryPath, "utf8"));
  const receipt = state.sourceMappings[adopted.id];
  assert.ok(receipt);
  const oldKey = createHash("sha256").update(`previous-spelling:${receipt.path}\0`).digest("hex");
  delete state.sourceMappings[adopted.id];
  receipt.sourceKey = oldKey;
  state.sourceMappings[oldKey] = receipt;
  writeFileSync(registryPath, JSON.stringify(state));

  await launch();
  assert.equal((await rows()).length, 2, "adopted source must not duplicate its destination");
  await archiveRow(ids[0], { hostId: "local", sessionId: ids[0] });
  checks.push("cold historical source archives through the real sidebar without opening");
  await archiveRow("QA previously migrated", imported.session);
  await rpc("RestoreSessionTarget", { ref: imported.session });
  assert.equal((await rpc("ArchiveSessionTarget", { source: adopted.source })).committed, true);
  checks.push("previous receipt identity archives through sidebar and explicit historical selector");
  await page.screenshot({ path: join(evidence, "archived.png") });
  await close();
  await launch();
  assert.equal((await rows()).length, 0, "restart must not resurrect retained sources");
  const trash = await rpc("ListTrashEntries", "", "", 50);
  assert.equal(trash.items.length, 2);
  for (const entry of trash.items) {
    assert.equal((await rpc("RestoreSessionTarget", { ref: entry.ref })).committed, true);
    const history = await rpc("ReadSessionHistory", entry.ref, "", 50);
    assert.equal(history.messages.length, 1);
    assert.match(history.messages[0].content, /^Archive acceptance history: qa-/);
  }
  assert.equal((await rows()).length, 2);
  for (const [file, bytes] of originals) assert.deepEqual(readFileSync(file), bytes, "original history bytes must be preserved");
  checks.push("restart preserves archive; restore recovers full content; original source bytes unchanged");
  await page.screenshot({ path: join(evidence, "restored.png") });
  await close();
  writeFileSync(join(evidence, "results.json"), JSON.stringify({ version, commit: build.commit, platform: process.platform, arch: process.arch, checks }, null, 2));
  console.log(`PASS ${checks.join("; ")}`);
} catch (error) {
  writeFileSync(join(evidence, "failure.txt"), String(error));
  if (page && !page.isClosed()) await page.screenshot({ path: join(evidence, "failure.png") }).catch(() => {});
  throw error;
} finally {
  if (application) await close();
}
