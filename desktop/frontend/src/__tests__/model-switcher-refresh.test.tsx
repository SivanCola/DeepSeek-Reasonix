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

const stale = deferred<ModelInfo[]>();
const fresh = deferred<ModelInfo[]>();
let calls = 0;
(window as unknown as { go: { main: { App: Record<string, unknown> } } }).go = {
  main: {
    App: {
      ModelsForTab: async () => {
        calls += 1;
        if (calls === 1) return stale.promise;
        if (calls === 2) return fresh.promise;
        return [{ ref: "glm-cn/glm-5.2", provider: "glm-cn", model: "glm-5.2", current: true }];
      },
    },
  },
};

const root = createRoot(document.getElementById("root")!);
await act(async () => {
  root.render(
    <LocaleProvider>
      <ModelSwitcher label="deepseek-v4-flash" tabId="tab-a" onPick={() => {}} />
    </LocaleProvider>,
  );
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

await act(async () => root.unmount());
console.log("model switcher refresh: PASS");
