import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>");
const previousWindow = globalThis.window;
globalThis.window = dom.window as unknown as Window & typeof globalThis;

type Workspace = { available: boolean; readOnly: boolean; sessions: never[]; shells: never[] };
const pending = new Map<string, (value: Workspace) => void>();
let calls = 0;
const fakeApp = {
  TerminalWorkspaceForTab(tabId: string) {
    calls += 1;
    return new Promise<Workspace>((resolve) => pending.set(tabId, resolve));
  },
};
(globalThis.window as unknown as { go: unknown }).go = { main: { App: fakeApp } };

const { resetTerminalStoreForTests, useTerminalStore } = await import("../store/terminal");
resetTerminalStoreForTests();

const oldRequest = useTerminalStore.getState().syncWorkspace("old-tab");
const newRequest = useTerminalStore.getState().syncWorkspace("new-tab");
pending.get("new-tab")?.({ available: true, readOnly: false, sessions: [], shells: [] });
await newRequest;
pending.get("old-tab")?.({ available: true, readOnly: false, sessions: [], shells: [] });
await oldRequest;
if (useTerminalStore.getState().tabId !== "new-tab") throw new Error("stale workspace response replaced the selected tab");
if (calls !== 2) throw new Error(`expected two workspace calls, got ${calls}`);

resetTerminalStoreForTests();
calls = 0;
const shared = useTerminalStore.getState().ensureReady("same-tab");
const sharedAgain = useTerminalStore.getState().ensureReady("same-tab");
pending.get("same-tab")?.({ available: true, readOnly: false, sessions: [], shells: [] });
await Promise.all([shared, sharedAgain]);
if (calls !== 1) throw new Error(`ensureReady duplicated the first request: ${calls}`);
process.stdout.write("PASS stale responses and first-ready deduplication\n");

if (previousWindow) globalThis.window = previousWindow;
else delete (globalThis as { window?: unknown }).window;
