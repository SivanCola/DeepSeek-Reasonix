// Run: tsx src/__tests__/model-switcher-refresh.test.tsx

import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { ModelSwitcher } from "../components/ModelSwitcher";
import { LocaleProvider } from "../lib/i18n";
import type { ModelInfo } from "../lib/types";

class TestResizeObserver {
  observe() {}
  disconnect() {}
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.ResizeObserver = TestResizeObserver as unknown as typeof ResizeObserver;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
Object.defineProperty(window, "matchMedia", {
  configurable: true,
  value: () => ({
    matches: true,
    media: "(prefers-reduced-motion: reduce)",
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  }),
});

const stale = deferred<ModelInfo[]>();
const fresh = deferred<ModelInfo[]>();
let calls = 0;
const picked: string[] = [];
let pickGate: ReturnType<typeof deferred<void>> | undefined;
let currentCatalog: ModelInfo[] = [
  { ref: "glm-cn/glm-5.2", provider: "glm-cn", model: "glm-5.2", current: true },
];
(window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
  main: {
    App: {
      ModelsForTab: async () => {
        calls += 1;
        if (calls === 1) return stale.promise;
        if (calls === 2) return fresh.promise;
        return currentCatalog;
      },
    },
  },
};

const root = createRoot(document.getElementById("root")!);
const renderSwitcher = (label: string, tabId: string) => (
  <LocaleProvider>
    <ModelSwitcher
      label={label}
      tabId={tabId}
      onPick={(ref) => {
        picked.push(ref);
        return pickGate?.promise;
      }}
    />
  </LocaleProvider>
);
await act(async () => {
  root.render(renderSwitcher("deepseek-v4-flash", "tab-a"));
});

await act(async () => {
  window.dispatchEvent(new Event("reasonix:model-catalog-changed"));
  fresh.resolve([{ ref: "glm-cn/glm-5.2", provider: "glm-cn", model: "glm-5.2", current: true }]);
  await fresh.promise;
});
await act(async () => {
  stale.resolve([{ ref: "deepseek/deepseek-v4-flash", provider: "deepseek", model: "deepseek-v4-flash", current: true }]);
  await stale.promise;
});
await act(async () => {
  (document.querySelector(".modelsw__trigger") as HTMLButtonElement).click();
  await new Promise((resolve) => setTimeout(resolve, 0));
});

const options = Array.from(document.querySelectorAll<HTMLElement>("[role='option']")).map((item) => item.textContent?.trim());
if (JSON.stringify(options) !== JSON.stringify(["glm-5.2"])) {
  throw new Error(`model catalog did not keep the fresh result: ${JSON.stringify(options)}`);
}
if (calls < 3) throw new Error(`expected mount, settings refresh, and open loads; got ${calls}`);

await act(async () => {
  (document.querySelector("[role='option'][aria-selected='true']") as HTMLButtonElement).click();
  await new Promise((resolve) => setTimeout(resolve, 0));
});
if (picked.length !== 0) throw new Error(`current model should be a no-op, picked ${JSON.stringify(picked)}`);
if ((document.querySelector(".modelsw__trigger") as HTMLButtonElement).getAttribute("aria-expanded") !== "false") {
  throw new Error("current-model no-op did not close the menu");
}

currentCatalog = [
  { ref: "glm-cn/glm-5.2", provider: "glm-cn", model: "glm-5.2", current: true },
  { ref: "deepseek/deepseek-v4-flash", provider: "deepseek", model: "deepseek-v4-flash", current: false },
];
pickGate = deferred<void>();
await act(async () => {
  (document.querySelector(".modelsw__trigger") as HTMLButtonElement).click();
  await new Promise((resolve) => setTimeout(resolve, 0));
});
await act(async () => {
  await new Promise((resolve) => setTimeout(resolve, 0));
});
const next = Array.from(document.querySelectorAll<HTMLButtonElement>("[role='option']"))
  .find((option) => option.textContent?.includes("deepseek-v4-flash"));
if (!next) throw new Error("pending-switch model option did not load");
await act(async () => {
  next.click();
  await new Promise((resolve) => setTimeout(resolve, 0));
});
if (picked[0] !== "deepseek/deepseek-v4-flash") {
  throw new Error(`non-current model was not selected: ${JSON.stringify(picked)}`);
}

// The backend catalog may still identify the outgoing GLM model as current
// while the first switch is pending. Selecting it now must enqueue a rollback.
await act(async () => {
  (document.querySelector(".modelsw__trigger") as HTMLButtonElement).click();
  await new Promise((resolve) => setTimeout(resolve, 0));
});
await act(async () => {
  await new Promise((resolve) => setTimeout(resolve, 0));
});
const rollback = document.querySelector<HTMLButtonElement>("[role='option'][aria-selected='true']");
if (!rollback) throw new Error("pending-switch rollback option did not load");
await act(async () => {
  rollback.click();
  await new Promise((resolve) => setTimeout(resolve, 0));
});
if (picked[1] !== "glm-cn/glm-5.2") {
  throw new Error(`pending current-model rollback was swallowed: ${JSON.stringify(picked)}`);
}

// Pending work belongs to tab A. Reusing the mounted switcher for tab B must
// not turn B's settled current model into another rollback request.
currentCatalog = [
  { ref: "provider-b/model-b", provider: "provider-b", model: "model-b", current: true },
];
await act(async () => {
  root.render(renderSwitcher("model-b", "tab-b"));
  await new Promise((resolve) => setTimeout(resolve, 0));
});
await act(async () => {
  (document.querySelector(".modelsw__trigger") as HTMLButtonElement).click();
  await new Promise((resolve) => setTimeout(resolve, 0));
});
await act(async () => {
  await new Promise((resolve) => setTimeout(resolve, 0));
});
const tabBCurrent = document.querySelector<HTMLButtonElement>("[role='option'][aria-selected='true']");
if (!tabBCurrent) throw new Error("tab B current model did not load");
await act(async () => {
  tabBCurrent.click();
  await new Promise((resolve) => setTimeout(resolve, 0));
});
if (picked.length !== 2) {
  throw new Error(`tab A pending picks leaked into tab B: ${JSON.stringify(picked)}`);
}

await act(async () => {
  pickGate?.resolve();
  await pickGate?.promise;
});

await act(async () => root.unmount());
console.log("model switcher refresh: PASS");
