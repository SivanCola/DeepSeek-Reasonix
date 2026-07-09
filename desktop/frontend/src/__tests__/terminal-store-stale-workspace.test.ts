// Run: tsx src/__tests__/terminal-store-stale-workspace.test.ts
//
// Regression for the terminal store's stale-workspace guard: ListTerminals /
// CreateTerminal responses that resolve after the store has moved on to a
// different workspace must be discarded, not written into the current UI.
// Without the guard, rapidly switching workspaces let an old request replace
// the new workspace's session list, and TerminalView then sent input to the
// stale session.

import { useTerminalStore } from "../store/terminal";
import type { TerminalSessionView } from "../lib/types";

// The bridge `app` proxy resolves window.go.main.App on every call, so the test
// backend is injected there (assigning onto the proxy itself is a no-op).
const fakeApp: Record<string, unknown> = {};
(globalThis as unknown as { window: unknown }).window = { go: { main: { App: fakeApp } } };

let passed = 0;
let failed = 0;

function ok(cond: boolean, label: string) {
  if (cond) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function session(id: string, workspaceRoot: string): TerminalSessionView {
  return { id, title: id, shell: "bash", cwd: workspaceRoot, workspaceRoot, createdAt: 0, running: true };
}

type Deferred<T> = { promise: Promise<T>; resolve: (v: T) => void };
function deferred<T>(): Deferred<T> {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => { resolve = r; });
  return { promise, resolve };
}

const flush = () => new Promise((r) => setTimeout(r, 0));

async function main() {
  console.log("\nterminal store stale-workspace guard");

  const store = useTerminalStore;
  const listCalls = new Map<string, Deferred<TerminalSessionView[]>>();
  fakeApp.ListTerminals = (workspaceRoot: string) => {
    const d = deferred<TerminalSessionView[]>();
    listCalls.set(workspaceRoot, d);
    return d.promise;
  };

  // Slow response for workspace A, then switch to workspace B before it lands.
  const syncA = store.getState().syncWorkspace("/ws-a", null, false);
  const syncB = store.getState().syncWorkspace("/ws-b", null, false);
  listCalls.get("/ws-b")!.resolve([session("b1", "/ws-b")]);
  await flush();
  ok(store.getState().workspaceRoot === "/ws-b", "store settled on workspace B");
  ok(store.getState().activeSessionId === "b1", "workspace B session is active");

  // The stale A response arrives last — it must be discarded.
  listCalls.get("/ws-a")!.resolve([session("a1", "/ws-a")]);
  await Promise.all([syncA, syncB]);
  await flush();
  ok(store.getState().workspaceRoot === "/ws-b", "stale sync kept workspace B");
  ok(store.getState().activeSessionId === "b1", "stale sync did not activate the old workspace's session");
  ok(store.getState().sessions.every((s) => s.workspaceRoot === "/ws-b"), "no stale sessions leaked into the list");

  // createSession that resolves after a workspace switch must not clobber it.
  const createCall = deferred<string>();
  fakeApp.CreateTerminal = () => createCall.promise;
  const create = store.getState().createSession();
  const syncC = store.getState().syncWorkspace("/ws-c", null, false);
  listCalls.get("/ws-c")!.resolve([]);
  await flush();
  createCall.resolve("b2");
  listCalls.set("/ws-b", deferred<TerminalSessionView[]>()); // createSession re-lists B
  await flush();
  listCalls.get("/ws-b")!.resolve([session("b1", "/ws-b"), session("b2", "/ws-b")]);
  await Promise.all([create, syncC]);
  await flush();
  ok(store.getState().workspaceRoot === "/ws-c", "stale createSession kept workspace C");
  ok(store.getState().sessions.length === 0, "workspace C session list not clobbered by stale create");
  ok(store.getState().activeSessionId === null, "stale created session not activated in workspace C");

  console.log(`\n${passed} passed, ${failed} failed`);
  if (failed > 0) process.exit(1);
}

void main();
