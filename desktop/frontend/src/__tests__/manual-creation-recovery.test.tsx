import assert from "node:assert/strict";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";
import { ManualSessionRecovery } from "../components/ManualSessionRecovery";
import { LocaleProvider } from "../lib/i18n";
import { manualCreationPresentation } from "../lib/manualCreationPresentation";
import type { ManualSessionCreationView } from "../generated/desktopContract.generated";
import { installDesktopHostStub } from "./desktopHostStub";

const item = { operationId: "recovery-test", scope: "global", phase: "starting" } as ManualSessionCreationView;
const progress = (status: string, slow = false) => ({ status, stage: "building_runtime", stageStartedAt: 1, elapsedMs: 0, slow });
assert.equal(manualCreationPresentation(item).key, "creation.pending");
assert.equal(manualCreationPresentation({ ...item, progress: progress("future-status") }).key, "creation.pending");
assert.equal(manualCreationPresentation({ ...item, progress: progress("running", true) }).key, "creation.slow");
assert.equal(manualCreationPresentation({ ...item, phase: "failed", progress: progress("queued") }).retryable, false);
assert.equal(manualCreationPresentation({ ...item, progress: progress("retrying_storage") }).retryable, false);
assert.equal(manualCreationPresentation({ ...item, progress: progress("retrying_storage") }).key, "creation.storageRetry");
assert.equal(manualCreationPresentation({ ...item, progress: { ...progress("retrying_storage"), stage: "persisting_result" } }).key, "creation.saving");
assert.equal(manualCreationPresentation({ ...item, progress: progress("waiting_lock") }).retryable, false, "host-owned waiting never asks users to retry");
assert.deepEqual(manualCreationPresentation({ ...item, progress: progress("waiting_workspace") }), { key: "creation.workspaceUnavailable", retryable: false });
assert.equal(manualCreationPresentation(item).retryable, false, "observing pending creation does not require a user choice");
for (const code of ["workspace_removed", "workspace_changed", "creation_cancelled", "creation_owner_conflict"]) {
  assert.equal(manualCreationPresentation({ ...item, phase: "failed", error: `session_operation:${code}:private detail` }).retryable, false, `${code} cannot recover by replaying the same operation`);
  assert.equal(manualCreationPresentation({ ...item, phase: "failed", error: `session_operation:${code}:private detail`, progress: progress("queued") }).key, "creation.queued", "active recovery progress supersedes an earlier failure");
}

const dom = new JSDOM("<div id='root'></div>", { url: "http://localhost" });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, IS_REACT_ACT_ENVIRONMENT: true });
let retryCount = 0;
let exports = 0;
let finish!: (view: ManualSessionCreationView) => void;
const reply = new Promise<ManualSessionCreationView>(resolve => { finish = resolve; });
const stub = installDesktopHostStub({
  ListManualSessionCreations: async () => [{ ...item, progress: progress("waiting_lock") }],
  RetryManualSessionCreation: async () => { retryCount++; return reply; },
  ExportManualCreationDiagnostics: async () => { exports++; return "report.json"; },
});
const root = createRoot(document.getElementById("root")!);
try {
  await act(async () => root.render(<LocaleProvider><ManualSessionRecovery /></LocaleProvider>));
  assert.match(document.body.textContent!, /continue automatically/);
  assert.equal([...document.querySelectorAll("button")].some(button => button.textContent === "Retry"), false);
  assert.equal(document.querySelector("details")?.open, false, "diagnostics are a secondary disclosure, not a creation decision");
  await act(async () => root.render(null));
  stub.commands.ListManualSessionCreations = async () => [{ ...item, phase: "failed", error: "session_operation:target_changed: /private/writer", progress: progress("blocked") }];
  await act(async () => root.render(<LocaleProvider><ManualSessionRecovery /></LocaleProvider>));
  assert.doesNotMatch(document.body.textContent!, /session_operation|\/private\/writer/, "internal failures stay out of the normal creation UI");
  const retry = document.querySelector("button")!;
  act(() => { retry.click(); retry.click(); });
  assert.equal(retryCount, 1, "duplicate retry is suppressed while the RPC is pending");
  assert.equal(retry.disabled, true);
  await act(async () => finish({ ...item, progress: progress("running") }));
  assert.match(document.body.textContent!, /Initializing session/);
  assert.equal(document.querySelectorAll("button").length, 1, "running work only exposes diagnostics");
  await act(async () => document.querySelector("button")!.click());
  assert.equal(exports, 1);
  console.log("PASS recovery states, retry coalescing, reply feedback and diagnostic export");
} finally {
  await act(async () => root.unmount());
  stub.uninstall();
  dom.window.close();
}
