import { randomBytes } from "node:crypto";
import { createConnection, createServer, type Socket } from "node:net";

// A live OS-owned connection avoids stale PID files, PID reuse and polling.
// This module also runs directly in Node >=24 development launch scripts.
export async function createLauncherOwner(): Promise<{ value: string; close(): void }> {
  const token = randomBytes(24).toString("hex");
  const sockets = new Set<Socket>();
  const server = createServer(socket => {
    sockets.add(socket); socket.unref(); socket.setTimeout(5000, () => socket.destroy());
    let input = "";
    socket.on("error", () => socket.destroy());
    socket.on("close", () => sockets.delete(socket));
    socket.on("data", chunk => {
      input += chunk.toString();
      if (input.length > 256) { socket.destroy(); return; }
      if (!input.includes("\n")) return;
      if (input !== token+"\n") { socket.destroy(); return; }
      socket.setTimeout(0); socket.write("owned\n");
    });
  });
  server.maxConnections = 16;
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  server.unref();
  const address = server.address();
  if (!address || typeof address === "string") { server.close(); throw new Error("launcher owner unavailable"); }
  return { value: `${address.port}:${token}`, close() {
    for (const socket of sockets) socket.destroy();
    server.close();
  } };
}

export function watchLauncherOwner(value: string | undefined, onLost: () => void): { ready: Promise<void>; dispose(): void } {
  if (!value) return { ready: Promise.resolve(), dispose() {} };
  let disposed = false, lost = false;
  let socket: Socket | undefined;
  let rejectReady!: (error: Error) => void;
  const ready = new Promise<void>((resolve, reject) => {
    rejectReady = reject;
    const match = /^(\d+):([a-f0-9]{48})$/.exec(value);
    if (!match || Number(match[1]) < 1 || Number(match[1]) > 65535) {
      queueMicrotask(() => fail()); return;
    }
    socket = createConnection({ host: "127.0.0.1", port: Number(match[1]) });
    socket.unref(); socket.setTimeout(5000, () => fail());
    socket.on("error", () => fail());
    socket.on("close", () => fail());
    socket.once("connect", () => socket?.write(match[2]+"\n"));
    let input = "";
    socket.on("data", chunk => {
      input += chunk.toString();
      if (input === "owned\n") { socket?.setTimeout(0); resolve(); }
      else if (input.length > 32 || input.includes("\n")) fail();
    });
  });
  function fail(): void {
    if (disposed || lost) return;
    lost = true; socket?.destroy();
    rejectReady(new Error("development launcher disconnected"));
    onLost();
  }
  // Runtime callers observe loss through onLost; tests may also await ready.
  void ready.catch(() => undefined);
  return { ready, dispose() { disposed = true; socket?.destroy(); } };
}

// Older test harnesses launch Electron directly without an ownership endpoint.
// On these development-only paths, detect reparenting as well as parent death.
// Managed launchers use the connection above, which also avoids PID reuse.
export function watchParentProcess(onLost: () => void, probe: {
  parentPID?: number; currentParent?: () => number; alive?: (pid: number) => boolean;
} = {}): { dispose(): void } {
  const parentPID = probe.parentPID ?? process.ppid;
  if (parentPID <= 1) return { dispose() {} };
  const alive = probe.alive ?? ((pid: number) => {
    try { process.kill(pid, 0); return true; }
    catch (error) { return (error as NodeJS.ErrnoException).code !== "ESRCH"; }
  });
  const timer = setInterval(() => {
    if ((probe.currentParent?.() ?? process.ppid) !== parentPID || !alive(parentPID)) {
      clearInterval(timer); onLost();
    }
  }, 1000);
  timer.unref();
  return { dispose() { clearInterval(timer); } };
}

// Keep ownership selection and its teardown together, including direct legacy
// launches. Packaged and intentionally detached shells have no launcher owner.
export function bindDevelopmentOwner(
  app: { isPackaged: boolean; once(event: "will-quit", listener: () => void): unknown },
  detached: boolean,
  log: { warn(message: string): void },
  requestQuit: () => void,
  env: NodeJS.ProcessEnv = process.env,
): void {
  const onLost = () => {
    log.warn("development launcher disconnected; saving and closing owned shell");
    requestQuit();
  };
  const endpoint = app.isPackaged || detached ? undefined : env.REASONIX_LAUNCH_OWNER;
  const owner = watchLauncherOwner(endpoint, onLost);
  const parent = !app.isPackaged && !detached && !endpoint ? watchParentProcess(onLost) : undefined;
  app.once("will-quit", () => { owner.dispose(); parent?.dispose(); });
}
