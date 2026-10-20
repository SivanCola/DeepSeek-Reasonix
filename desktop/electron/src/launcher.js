"use strict";

// A development shell belongs to the process that launched it: `pnpm start`,
// a live test, an agent's background job. Killed outright, that launcher takes
// nothing with it, so the shell has to notice it is gone and leave on its own.
// Packaged shells, and shells a relaunch handed over, belong to nobody.
const HANDED_OVER = "REASONIX_STUDIO_HANDED_OVER";
const EVERY_MS = 1000;

// handOver marks the next instance as nobody's. The launcher waits on this
// process and exits with it, so a relaunched shell would otherwise read its own
// start as the launcher's death.
function handOver(env = process.env) {
  env[HANDED_OVER] = "1";
}

function alive(pid) {
  try {
    process.kill(pid, 0);
    return true;
  } catch (err) {
    return err?.code === "EPERM";
  }
}

// POSIX gives an orphan a new parent, which settles it. Windows leaves the dead
// pid as the parent, so there it is probed instead, and a pid already reused
// leaves the shell running as it would without this watch.
function watchLauncher({ packaged, env = process.env, onLost, parent = () => process.ppid, alive: isAlive = alive, everyMs = EVERY_MS }) {
  const handedOver = env[HANDED_OVER] === "1";
  delete env[HANDED_OVER];
  const launcher = parent();
  if (packaged || handedOver || launcher <= 1) return { dispose() {} };
  const timer = setInterval(() => {
    if (parent() === launcher && isAlive(launcher)) return;
    clearInterval(timer);
    onLost();
  }, everyMs);
  timer.unref?.();
  return { dispose: () => clearInterval(timer) };
}

// consoleEcho writes to a console the launcher may already have closed. An
// EPIPE nobody handles opens Electron's modal error box, which stops the main
// process; shell.log and host.log keep every line, so a failed or backed-up
// console is dropped rather than retried.
function consoleEcho(stream) {
  let failed = false;
  let blocked = false;
  stream.on("error", () => {
    failed = true;
  });
  stream.on("drain", () => {
    blocked = false;
  });
  return (chunk) => {
    if (failed || blocked || stream.destroyed || stream.writableEnded) return;
    try {
      blocked = !stream.write(chunk);
    } catch {
      failed = true;
    }
  };
}

module.exports = { watchLauncher, handOver, consoleEcho };
