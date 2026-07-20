/**
 * Unit tests for Remote AppBridge method dispatch (no real network).
 * Run via: pnpm --dir desktop/frontend test remote-app-bridge
 */

import assert from "node:assert/strict";
import { afterEach, beforeEach, describe, it } from "node:test";

// Minimal window stub for gateway mode detection.
const g = globalThis as typeof globalThis & {
  window?: { __REASONIX_REMOTE__?: { mode: string }; go?: unknown };
};

describe("dispatchRemoteBridgeMethod", () => {
  beforeEach(() => {
    g.window = { __REASONIX_REMOTE__: { mode: "gateway" } };
  });
  afterEach(() => {
    delete g.window;
  });

  it("maps Submit family and Approve/Cancel/ListTabs to remote handlers", async () => {
    // Dynamic import after window stub so module sees gateway mode.
    const { dispatchRemoteBridgeMethod, clearRemoteGatewaySessionCache } = await import("../lib/remoteAppBridge");
    clearRemoteGatewaySessionCache();

    // Without gateway session credentials, handlers fail — but dispatch must
    // still claim them (non-null) so bridge does not fall through to local.
    for (const method of [
      "Submit",
      "SubmitToTab",
      "SubmitDisplay",
      "SubmitDisplayToTab",
      "Cancel",
      "CancelTab",
      "Approve",
      "ApproveTab",
      "AnswerQuestion",
      "AnswerQuestionForTab",
      "CompactForTab",
      "RewindForTab",
      "SetModelForTab",
      "ListTabs",
      "ListRemoteDir",
      "ReadRemoteFile",
      "WriteRemoteFile",
    ]) {
      const p = dispatchRemoteBridgeMethod(method, ["a", "b", "c", true, false]);
      assert.ok(p instanceof Promise, `${method} should return a Promise`);
      await p.catch(() => undefined); // expected without live gateway
    }

    // Local-only settings should resolve as no-ops without network.
    await assert.doesNotReject(() => dispatchRemoteBridgeMethod("SetModeForTab", ["t", "yolo"])!);
    await assert.doesNotReject(() => dispatchRemoteBridgeMethod("SteerForTab", ["t", "x"])!);

    // Unknown methods fall through.
    assert.equal(dispatchRemoteBridgeMethod("Platform", []), null);
  });

  it("returns null when not a remote window", async () => {
    g.window = {};
    const { dispatchRemoteBridgeMethod, clearRemoteGatewaySessionCache } = await import("../lib/remoteAppBridge");
    clearRemoteGatewaySessionCache();
    assert.equal(dispatchRemoteBridgeMethod("Submit", ["hi"]), null);
  });
});
