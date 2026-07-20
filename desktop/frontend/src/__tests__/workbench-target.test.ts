/**
 * Workbench target helpers — dispatch shape only (no live Wails).
 */
import assert from "node:assert/strict";
import { describe, it, beforeEach, afterEach } from "node:test";

const g = globalThis as any;

describe("workbenchTarget", () => {
  beforeEach(() => {
    g.window = {
      go: {
        main: {
          App: {
            WorkbenchActiveTarget: async () => ({ kind: "local", identityGen: 1, requestSeq: 1 }),
            WorkbenchLastRemoteHint: async () => ({ hostId: "lab", workspace: "/w" }),
            WorkbenchSwitchLocal: async () => ({ kind: "local", identityGen: 2, requestSeq: 2 }),
            WorkbenchConnectRemote: async () => undefined,
            WorkbenchDisconnectRemote: async () => undefined,
            WorkbenchRemoteRequest: async (_m: unknown, body: unknown) =>
              JSON.stringify({ ok: true, body }),
            WorkbenchResolveProviderTrust: async () => undefined,
            WorkbenchPendingProviderTrust: async () => null,
          },
        },
      },
    };
  });
  afterEach(() => {
    delete g.window;
  });

  it("reads active target and remote hint", async () => {
    const mod = await import("../lib/workbenchTarget");
    const active = await mod.fetchActiveTarget();
    assert.equal(active.kind, "local");
    const hint = await mod.fetchLastRemoteHint();
    assert.equal(hint?.hostId, "lab");
  });

  it("proxies remote request JSON", async () => {
    const mod = await import("../lib/workbenchTarget");
    const res = (await mod.remoteRequest("session/list", { x: 1 })) as { ok: boolean };
    assert.equal(res.ok, true);
  });
});
