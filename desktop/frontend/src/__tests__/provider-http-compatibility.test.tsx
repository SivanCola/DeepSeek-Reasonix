import assert from "node:assert/strict";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";
import { installDesktopHostStub } from "./desktopHostStub";
import { LocaleProvider, preloadLocale } from "../lib/i18n";
import { ProviderHTTPCompatibilityActions } from "../components/ProviderHTTPCompatibilityActions";
import { useAppNavigationStore } from "../store/appNavigation";
import type { ModelSettingsChange, ModelSettingsResult } from "../lib/types";

const dom = new JSDOM('<div id="root"></div>', { url: "http://localhost/", pretendToBeVisual: true });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, IS_REACT_ACT_ENVIRONMENT: true });
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
await preloadLocale("en");
const calls: ModelSettingsChange[] = [];
let fail = false, missing = false;
let resolveSave: ((value: ModelSettingsResult) => void) | undefined;
installDesktopHostStub({
  Settings: async () => ({ modelSettingsFingerprint: "revision-1", providers: missing ? [] : [{ name: "connection-id", http1Only: false }] }),
  ApplyModelSettings: async (change: ModelSettingsChange) => {
    calls.push(change);
    if (fail) return { persisted: false, issues: [{ code: "conflict", message: "Reload settings" }] };
    return new Promise<ModelSettingsResult>(resolve => { resolveSave = resolve; });
  },
  GetModelSettingsRequest: async () => { throw new Error("unexpected receipt lookup"); },
});
const root = createRoot(document.getElementById("root")!);
let key = 0;
const render = async (kind = "transport_protocol", hostId = "local", providerId = "connection-id", transportCode = "PROTOCOL_ERROR") => {
  await act(async () => root.render(<LocaleProvider><ProviderHTTPCompatibilityActions key={++key} diagnostic={{ kind, providerId, transportCode }} tabId="tab-one" hostId={hostId} /></LocaleProvider>));
};
for (const code of ["", "GOAWAY", "ENHANCE_YOUR_CALM", "INTERNAL_ERROR", "REFUSED_STREAM", "NO_ERROR"]) {
  await render("transport_protocol", "local", "connection-id", code);
  assert.equal(document.querySelector("button"), null, `${code} must not suggest a protocol change`);
}
await render("unknown"); assert.equal(document.querySelector("button"), null);
await render("transport_protocol", "remote-host"); assert.equal(document.querySelector("button"), null, "remote identities cannot edit local providers");
await render("transport_protocol", "local", ""); assert.equal(document.querySelector("button"), null);
await render();
const button = document.querySelector<HTMLButtonElement>("button")!;
await act(async () => { button.click(); button.click(); });
assert.equal(calls.length, 1, "double click writes once");
assert.equal(calls[0].kind, "http1_compatibility");
assert.equal(calls[0].expectedFingerprint, "revision-1");
assert.equal((calls[0] as {name:string}).name, "connection-id");
assert.equal(button.disabled, true);
await act(async () => resolveSave!({requestId:calls[0].requestId, persisted:true, revision:"revision-2", application:"pending", targets:[], issues:[], appliedCatalogs:[]}));
assert.match(document.querySelector('[role="status"]')!.textContent!, /saved|已保存/);
assert.equal(calls.length,1,"saving compatibility does not resend or submit another operation");
await act(async () => document.querySelector<HTMLButtonElement>("button")!.click());
assert.equal(useAppNavigationStore.getState().settingsFocus?.target,"model-access");
assert.equal((useAppNavigationStore.getState().settingsFocus as {providerName:string}).providerName,"connection-id");
assert.equal(useAppNavigationStore.getState().page.kind,"settings");
fail = true;
await render();
await act(async () => document.querySelector<HTMLButtonElement>("button")!.click());
assert.ok(document.querySelector('[role="alert"]'),"save conflicts remain visible");
assert.equal(document.querySelector('[role="status"]'),null,"a failed save never reports success");
missing = true;
await render();
await act(async () => document.querySelector<HTMLButtonElement>("button")!.click());
assert.equal(calls.length,2,"deleted connections do not write settings");
await act(async () => root.unmount());
dom.window.close();
console.log("provider HTTP compatibility actions: ok");
