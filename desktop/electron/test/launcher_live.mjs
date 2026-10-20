// Drives the real shell and kernel through the ways a development launch ends:
// its launcher killed outright, before and after the handshake, its console
// closed underneath it, and an ordinary stop. Each runs in a throwaway data home.
// Run `pnpm build:host` first, then `pnpm launcher-live`.
import assert from "node:assert/strict";
import { spawn, execFileSync } from "node:child_process";
import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const appDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const { hostBinary } = require("../src/layout.js");
const host = hostBinary({ packaged: false, dirname: path.join(appDir, "src"), platform: process.platform, env: process.env });
const cli = path.join(path.dirname(require.resolve("electron")), "cli.js");
if (!existsSync(host)) throw new Error(`no kernel at ${host}; run pnpm build:host first`);

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const alive = (pid) => {
  try {
    process.kill(pid, 0);
    return true;
  } catch (err) {
    return err.code === "EPERM";
  }
};

async function until(predicate, what, ms) {
  const deadline = Date.now() + ms;
  while (Date.now() < deadline) {
    if (predicate()) return;
    await sleep(100);
  }
  throw new Error(what);
}

// userData is <platform app data>/Reasonix Studio/<instance id>, and the id is
// the kernel's answer for this data home.
function profileDir(env) {
  const id = execFileSync(host, ["-instance-id"], { env, encoding: "utf8" }).trim();
  const base =
    process.platform === "darwin" ? path.join(os.homedir(), "Library", "Application Support")
    : process.platform === "win32" ? process.env.APPDATA
    : process.env.XDG_CONFIG_HOME || path.join(os.homedir(), ".config");
  return path.join(base, "Reasonix Studio", id);
}

async function scenario(name, { kernel = host, end }) {
  const home = mkdtempSync(path.join(os.tmpdir(), "reasonix-launcher-live-"));
  const page = path.join(home, "page");
  mkdirSync(page);
  writeFileSync(path.join(page, "index.html"), "<!doctype html><title>launcher live</title>");
  const env = { ...process.env, REASONIX_HOME: home, REASONIX_STATE_HOME: path.join(home, "state"), REASONIX_STUDIO_PAGE: page, REASONIX_STUDIO_HOST: kernel };
  const profile = profileDir(env);
  const shellLog = () => {
    try {
      return readFileSync(path.join(profile, "logs", "shell.log"), "utf8");
    } catch {
      return "";
    }
  };
  const launcher = spawn(process.execPath, [cli, appDir], { cwd: appDir, env, stdio: ["ignore", "pipe", "pipe"] });
  launcher.stdout.resume();
  launcher.stderr.resume();
  const pids = [];
  try {
    await until(() => /host: pid \d+/.test(shellLog()), "the shell never started its kernel", 60000);
    const log = shellLog();
    pids.push(Number(/shell: start .* pid=(\d+)/.exec(log)[1]), Number(/host: pid (\d+)/.exec(log)[1]));
    await end({ launcher, shellLog });
    await until(() => pids.every((pid) => !alive(pid)), `${name}: the shell or its kernel outlived the launch`, 20000);
    assert.doesNotMatch(shellLog(), /uncaught/i);
    console.log(`  ok   ${name}`);
  } catch (err) {
    console.log(`  FAIL ${name}: ${err.message}\n${shellLog().split("\n").slice(-10).join("\n")}`);
    process.exitCode = 1;
  } finally {
    launcher.kill("SIGKILL");
    for (const pid of pids) if (alive(pid)) process.kill(pid, "SIGKILL");
    await sleep(500);
    rmSync(profile, { recursive: true, force: true });
    rmSync(home, { recursive: true, force: true });
  }
}

const handshaken = ({ shellLog }) => until(() => /host: handshake/.test(shellLog()), "no handshake", 60000);

await scenario("a launcher killed after the handshake takes the shell with it", {
  end: async (run) => {
    await handshaken(run);
    run.launcher.kill("SIGKILL");
    await until(() => /launcher gone/.test(run.shellLog()), "the shell never noticed its launcher was gone", 10000);
  },
});

// A kernel that takes its time to handshake: the launch is still starting when
// its launcher goes.
if (process.platform !== "win32") {
  const dir = mkdtempSync(path.join(os.tmpdir(), "reasonix-slow-host-"));
  const slow = path.join(dir, "host.sh");
  writeFileSync(slow, `#!/bin/sh\nsleep 3\nexec ${JSON.stringify(host)} "$@"\n`, { mode: 0o755 });
  await scenario("a launcher killed before the handshake takes the starting shell with it", {
    kernel: slow,
    end: async (run) => {
      assert.doesNotMatch(run.shellLog(), /host: handshake/);
      run.launcher.kill("SIGKILL");
    },
  });
  rmSync(dir, { recursive: true, force: true });
}

await scenario("a shell whose console was closed still stops when asked", {
  end: async (run) => {
    await handshaken(run);
    run.launcher.stdout.destroy();
    run.launcher.stderr.destroy();
    // The kernel logs a request every few seconds, and each one is echoed.
    await sleep(8000);
    run.launcher.kill("SIGTERM");
  },
});

await scenario("a launcher asked to stop stops the shell", {
  end: async (run) => {
    await handshaken(run);
    run.launcher.kill("SIGTERM");
  },
});
