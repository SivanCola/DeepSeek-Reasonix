// Real Electron + Go service, isolated homes, no debugger (Node's inspector can
// itself keep a quitting main process alive). No provider requests are made.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createRequire } from "node:module";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const service = process.env.REASONIX_DESKTOP_SERVICE || resolve(root, "../build/bin", process.platform === "win32" ? "reasonix-desktop-service.exe" : "reasonix-desktop-service");
const alive = pid => { try { process.kill(pid, 0); return true; } catch { return false; } };
async function until(predicate, label, timeout = 20000) {
  const deadline = Date.now()+timeout;
  while (Date.now() < deadline) {
    if (predicate()) return;
    await new Promise(resolveWait => setTimeout(resolveWait, 25));
  }
  throw new Error(label);
}

for (const { signal, ready, direct } of [
  { signal: "SIGTERM", ready: true },
  { signal: "SIGKILL", ready: true },
  { signal: "SIGKILL", ready: false },
  { signal: "SIGKILL", ready: true, direct: true },
]) {
  const home = mkdtempSync(join(tmpdir(), "reasonix-process-smoke-"));
  const logPath = join(home, "desktop-shell/logs/shell.log");
  const readLog = () => { try { return readFileSync(logPath, "utf8"); } catch { return ""; } };
  const cli = join(dirname(createRequire(import.meta.url).resolve("electron")), "cli.js");
  const launcher = spawn(process.execPath, direct ? [cli, root] : [join(root, "scripts/start.mjs")], { env: { ...process.env,
    REASONIX_HOME: home, REASONIX_STATE_HOME: home, REASONIX_CACHE_HOME: join(home, "cache"),
    REASONIX_DEV: "1", REASONIX_DESKTOP_SERVICE: service,
    REASONIX_LAUNCH_OWNER: "", REASONIX_ELECTRON_DEV_URL: "", REASONIX_DETACHED_LAUNCH: "",
  }, stdio: ["ignore", "pipe", "pipe"] });
  launcher.stdout.resume(); launcher.stderr.resume();
  let shellPID = 0, servicePID = 0;
  try {
    const stage = ready ? /desktop service ready:.*?pid (\d+)/ : /desktop service spawned: pid (\d+)/;
    await until(() => stage.test(readLog()), "service did not reach the expected startup stage", 60000);
    const initial = readLog();
    shellPID = Number(/shell starting: pid (\d+)/.exec(initial)?.[1]);
    servicePID = Number(stage.exec(initial)?.[1]);
    assert.ok(shellPID && servicePID);
    const label = direct ? "direct" : ready ? "ready" : "startup";
    console.log("Checking "+signal+" "+label+": shell="+shellPID+" service="+servicePID);
    if (ready) await new Promise(resolveWait => setTimeout(resolveWait, 300));
    launcher.stderr.destroy();
    launcher.kill(signal);
    await until(() => !alive(shellPID) && !alive(servicePID), "owned Electron or Go service survived launcher termination");
    const log = readLog();
    assert.match(log, /shutdown complete/);
    assert.match(log, /cleanup browser views complete/);
    if (signal === "SIGKILL") assert.match(log, /development launcher disconnected/);
    assert.doesNotMatch(log, /uncaught exception: write EPIPE|uncaught exception: write EIO/);
    assert.ok(log.length < 100000, "broken stderr produced runaway logs");
    console.log("PASS real launcher "+signal+" "+label+" + broken stderr: saved, awaited cleanup, Electron and Go service exited");
  } catch (error) {
    console.error(readLog().split("\n").slice(-15).join("\n"));
    throw error;
  } finally {
    launcher.kill("SIGKILL");
    for (const pid of [shellPID, servicePID]) if (pid && alive(pid)) process.kill(pid, "SIGKILL");
    rmSync(home, { recursive: true, force: true });
  }
}
