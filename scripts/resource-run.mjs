#!/usr/bin/env node
// Common package/Make targets use this entry automatically; other commands may
// opt in. Admission is always validated by Go, including inherited leases.
import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const args = process.argv.slice(2);
if (!args.length) throw new Error("usage: node scripts/resource-run.mjs -- command [args...]");
let active;
let stopping = false;
for (const signal of ["SIGTERM", "SIGINT"]) process.on(signal, () => {
  stopping = true;
  if (!active || active.exitCode !== null || active.signalCode !== null) return;
  if (process.platform === "win32") active.kill(signal);
  else { try { process.kill(-active.pid, signal); } catch (error) { if (error.code !== "ESRCH") throw error; } }
});

function run(command, argv, options) {
  return new Promise((resolveExit, reject) => {
    active = spawn(command, argv, { stdio: "inherit", detached: process.platform !== "win32", ...options });
    active.once("error", reject);
    active.once("exit", code => resolveExit(code ?? 1));
  });
}

// CI's explicit job/shard budgets remain authoritative. Only automatically
// wrapped package scripts skip local admission; an explicit wrapper never does.
const ciScript = ["true", "1"].includes(process.env.CI) && args[0] === "--script" && args.length === 2;
if (ciScript) {
  process.exitCode = await run(args[1], [], { env: process.env, shell: true });
} else {
  // pnpm appends script flags after the command. Keep them out of the helper's
  // flag parser and let the platform script launcher pass them as literal argv.
  if (args[0] === "--script" && args.length > 2) args.splice(2, 0, "--");
  // Build only the small helper with Go's compilation cache. Execute the binary
  // directly: go run would collapse every non-zero test exit code to 1.
  const temp = await mkdtemp(join(tmpdir(), "reasonix-resource-run-"));
  try {
    const binary = join(temp, process.platform === "win32" ? "resource.exe" : "resource");
    const built = await run("go", ["build", "-p=1", "-o", binary, "./cmd/reasonix-resource"], {
      // Cross-build variables belong to the requested command, not this native
      // launcher. The child below still receives the caller's original env.
      cwd: root, env: { ...process.env, GOMAXPROCS: "2", CGO_ENABLED: "0", GOFLAGS: "",
        GOOS: process.platform === "win32" ? "windows" : process.platform,
        GOARCH: ({ x64: "amd64", ia32: "386" })[process.arch] ?? process.arch, GOAMD64: "v1" },
    });
    process.exitCode = built;
    if (!built && !stopping) process.exitCode = await run(binary, ["--cwd", process.cwd(), ...args], { env: process.env });
    else if (stopping) process.exitCode = 1;
  } finally { await rm(temp, { recursive: true, force: true }); }
}
