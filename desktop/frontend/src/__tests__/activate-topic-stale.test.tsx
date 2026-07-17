// Run: tsx src/__tests__/activate-topic-stale.test.tsx
//
// Locks in last-click-wins for single-surface topic activation (#6607): when
// a newer navigation starts while app.ActivateTopic is still in flight, the
// stale completion must neither flip the visible tab away from the user's
// last click nor delete the newer surface's cached state (the single-surface
// prune removes every other tab state, blanking the visible transcript).

import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import type { AppBindings } from "../lib/bridge";
import { useController } from "../lib/useController";
import type { BalanceInfo, CheckpointMeta, ContextInfo, EffortInfo, HistoryMessage, JobView, Meta, TabMeta } from "../lib/types";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  ok(actual === expected, `${label}${actual === expected ? "" : `: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`}`);
}

function flushPromises(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

async function waitFor(label: string, predicate: () => boolean) {
  for (let attempt = 0; attempt < 30; attempt += 1) {
    await act(async () => {
      await flushPromises();
    });
    if (predicate()) return;
  }
  throw new Error(`timed out waiting for ${label}`);
}

function tabMeta(id: string, overrides: Partial<TabMeta> = {}): TabMeta {
  const workspaceRoot = `/repo/${id}`;
  return {
    id,
    scope: "project",
    workspaceRoot,
    workspaceName: id,
    workspacePath: workspaceRoot,
    gitBranch: "main",
    topicId: `topic-${id}`,
    topicTitle: id,
    sessionPath: `${workspaceRoot}/sessions/${id}.jsonl`,
    label: `model-${id}`,
    ready: true,
    running: false,
    mode: "normal",
    toolApprovalMode: "ask",
    tokenMode: "full",
    active: false,
    cwd: workspaceRoot,
    ...overrides,
  };
}

function metaFor(tab: TabMeta): Meta {
  return {
    label: tab.label,
    ready: tab.ready,
    startupErr: tab.startupErr,
    eventChannel: "agent:event",
    cwd: tab.cwd || tab.workspaceRoot,
    workspaceRoot: tab.workspaceRoot,
    workspaceName: tab.workspaceName,
    workspacePath: tab.workspacePath,
    gitBranch: tab.gitBranch,
    autoApproveTools: false,
    bypass: false,
    collaborationMode: tab.collaborationMode ?? "normal",
    toolApprovalMode: tab.toolApprovalMode ?? "ask",
    tokenMode: tab.tokenMode ?? "full",
    goal: "",
    goalStatus: "stopped",
  };
}

function userMessage(content: string): HistoryMessage {
  return { role: "user", content };
}

console.log("\nactivate topic stale completion");

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

const context: ContextInfo = { used: 12, window: 100, sessionTokens: 12 };
const effort: EffortInfo = { supported: true, current: "auto", default: "auto", levels: ["auto"] };
const balance: BalanceInfo = { available: false, display: "" };
const jobs: JobView[] = [];
const checkpoints: CheckpointMeta[] = [];
const tabA = tabMeta("tab-a", { active: true });
const tabX = tabMeta("tab-x");
const tabY = tabMeta("tab-y");
let backendActiveId = "tab-a";
const activateXGate = deferred<void>();
const tabsById = new Map([tabA, tabX, tabY].map((tab) => [tab.id, tab]));

function currentTabs(): TabMeta[] {
  return Array.from(tabsById.values()).map((tab) => ({ ...tab, active: tab.id === backendActiveId }));
}

window.runtime = {
  EventsOn: () => () => {},
  BrowserOpenURL: () => {},
};
window.go = {
  main: {
    App: {
      ListTabs: async () => currentTabs(),
      MetaForTab: async (tabID: string) => metaFor(tabsById.get(tabID) ?? tabA),
      ContextUsageForTab: async () => context,
      EffortForTab: async () => effort,
      BalanceForTab: async () => balance,
      JobsForTab: async () => jobs,
      CheckpointsForTab: async () => checkpoints,
      HistoryForTab: async (tabID: string) => {
        if (tabID === "tab-x") return [userMessage("history X")];
        if (tabID === "tab-y") return [userMessage("history Y")];
        return [userMessage("history A")];
      },
      HistoryPageForTab: async (tabID: string) => {
        const messages = await window.go.main.App.HistoryForTab(tabID);
        return { messages, startTurn: 0, endTurn: messages.length, totalTurns: messages.length, hasOlder: false };
      },
      HistoryCheckpointTurnsForTab: async () => [],
      ActivateTopic: async (_scope: string, workspaceRoot: string, topicId: string) => {
        const target = Array.from(tabsById.values()).find((tab) => tab.workspaceRoot === workspaceRoot && tab.topicId === topicId) ?? tabA;
        if (target.id === "tab-x") await activateXGate.promise;
        backendActiveId = target.id;
        return { ...target, active: true };
      },
      SetActiveTab: async (tabID: string) => {
        backendActiveId = tabID;
      },
      ReplayPendingPrompts: async () => {},
    } as Partial<AppBindings> as AppBindings,
  },
};

type Controller = ReturnType<typeof useController>;
let controller: Controller | undefined;

function Probe() {
  controller = useController();
  return null;
}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");
const root = createRoot(rootEl);

await act(async () => {
  root.render(<Probe />);
  await flushPromises();
});
await waitFor("initial active tab", () => controller?.activeTabId === "tab-a");

// Click topic X: the backend call hangs (slow prune / disk).
let activateX: Promise<TabMeta> | undefined;
await act(async () => {
  activateX = controller?.activateTopic("project", tabX.workspaceRoot, tabX.topicId ?? "");
  await flushPromises();
});
eq(controller?.activeTabId, "tab-a", "held activation does not flip the tab early");

// The user clicks topic Y before X's backend call returns; Y resolves first.
await act(async () => {
  await controller?.activateTopic("project", tabY.workspaceRoot, tabY.topicId ?? "");
  await flushPromises();
});
await waitFor("Y is active with its history", () =>
  controller?.activeTabId === "tab-y" && controller.state.items.some((item) => item.kind === "user" && item.text === "history Y"));

// X's stale completion lands after Y applied. Last click must win.
await act(async () => {
  activateXGate.resolve();
  await activateX;
  await flushPromises();
});
await act(async () => {
  await flushPromises();
});
eq(controller?.activeTabId, "tab-y", "stale activation must not flip the visible tab");
ok(controller?.state.items.some((item) => item.kind === "user" && item.text === "history Y") === true,
  "stale activation must not delete the visible tab's cached state");

// A fresh activation afterwards still applies normally (guard is not sticky).
await act(async () => {
  await controller?.activateTopic("project", tabX.workspaceRoot, tabX.topicId ?? "");
  await flushPromises();
});
await waitFor("X activates cleanly on a fresh click", () => controller?.activeTabId === "tab-x");

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
