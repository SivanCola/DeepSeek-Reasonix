// Run: tsx src/__tests__/config-load-warnings.test.ts

import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings } from "../lib/bridge";
import {
  configLoadWarningsKey,
  normalizeConfigLoadWarnings,
  subscribeConfigLoadWarnings,
  useConfigLoadWarnings,
} from "../lib/useConfigLoadWarnings";

let passed = 0;
let failed = 0;

function equal(actual: unknown, expected: unknown, label: string) {
  if (JSON.stringify(actual) === JSON.stringify(expected)) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

console.log("\nconfig load warnings");

equal(
  normalizeConfigLoadWarnings([" warning one ", "", 42, "warning one", "warning two"]),
  ["warning one", "warning two"],
  "Wails payload normalization keeps unique non-empty strings",
);
equal(configLoadWarningsKey(["a", "b"]), '["a","b"]', "warning fingerprints are stable");
equal(configLoadWarningsKey([]), "", "empty warning lists have no fingerprint");

let runtimeHandler: ((payload?: unknown) => void) | undefined;
let unsubscribed = false;
const runtimeHandlers = new Set<(payload?: unknown) => void>();
const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', { url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
window.runtime = {
  EventsOn: (name: string, cb: (...data: unknown[]) => void) => {
    if (name === "config:load-warnings") {
      runtimeHandler = cb;
      runtimeHandlers.add(cb);
    }
    return () => {
      runtimeHandlers.delete(cb);
      unsubscribed = true;
    };
  },
  BrowserOpenURL: () => {},
};
window.go = { main: { App: {} as AppBindings } };

function emitWarnings(payload: unknown) {
  runtimeHandlers.forEach((handler) => handler(payload));
}

const received: string[][] = [];
const stop = subscribeConfigLoadWarnings((warnings) => received.push(warnings));
runtimeHandler?.([" current warning ", null, "current warning"]);
runtimeHandler?.([]);
equal(received, [["current warning"]], "runtime bridge forwards normalized non-empty warnings");
stop();
equal(unsubscribed, true, "runtime warning subscription is disposable");

type WarningHook = ReturnType<typeof useConfigLoadWarnings>;
let warningHook: WarningHook | undefined;
function Probe() {
  warningHook = useConfigLoadWarnings();
  return null;
}

const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("missing test root");
const root = createRoot(rootElement);
await act(async () => { root.render(React.createElement(Probe)); });

const staleSnapshotRevision = warningHook?.beginSnapshot() ?? -1;
await act(async () => { emitWarnings(["runtime warning"]); });
warningHook?.applySnapshot([], staleSnapshotRevision);
equal(warningHook?.configLoadWarnings, ["runtime warning"], "stale startup snapshots cannot clear runtime warnings");

await act(async () => { warningHook?.dismiss(); });
await act(async () => { emitWarnings(["runtime warning"]); });
equal(warningHook?.configLoadWarnings, [], "dismissed warnings stay hidden across repeated session builds");

await act(async () => { warningHook?.reload([]); });
await act(async () => { emitWarnings(["runtime warning"]); });
equal(warningHook?.configLoadWarnings, ["runtime warning"], "a successful reload resets warning deduplication");
await act(async () => { root.unmount(); });

if (failed > 0) {
  process.stdout.write(`\n${failed} failed, ${passed} passed\n`);
  process.exit(1);
}
process.stdout.write(`\n${passed} passed\n`);
