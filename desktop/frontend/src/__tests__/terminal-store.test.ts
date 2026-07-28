import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>");
const previousWindow = globalThis.window;
globalThis.window = dom.window as unknown as Window & typeof globalThis;

type Session = {
  id: string;
  title: string;
  shell: string;
  cwd: string;
  createdAt: number;
  running: boolean;
};
type Workspace = { available: boolean; readOnly: boolean; sessions: Session[]; shells: { id: string; label: string }[] };
const pending = new Map<string, (value: Workspace) => void>();
const createPending: Array<(value: Session) => void> = [];
let calls = 0;
const fakeApp = {
  TerminalWorkspaceForTab(tabId: string) {
    calls += 1;
    return new Promise<Workspace>((resolve) => pending.set(tabId, resolve));
  },
  CreateTerminalForTab() {
    return new Promise<Session>((resolve) => createPending.push(resolve));
  },
  async CloseTerminalForTab() {},
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

resetTerminalStoreForTests();
const readyWorkspace: Workspace = {
  available: true,
  readOnly: false,
  sessions: [],
  shells: [{ id: "default", label: "Default shell" }],
};
useTerminalStore.setState({
  tabId: "create-tab",
  workspace: readyWorkspace,
  loading: false,
  activeSessionId: null,
});
const firstCreate = useTerminalStore.getState().createSession("create-tab");
const secondCreate = useTerminalStore.getState().createSession("create-tab");
await Promise.resolve();
await Promise.resolve();
if (createPending.length !== 2) throw new Error(`expected two create calls, got ${createPending.length}`);
const firstSession: Session = {
  id: "first",
  title: "first",
  shell: "default",
  cwd: ".",
  createdAt: 1,
  running: true,
};
const secondSession: Session = {
  id: "second",
  title: "second",
  shell: "default",
  cwd: ".",
  createdAt: 2,
  running: true,
};
createPending[1]?.(secondSession);
await secondCreate;
createPending[0]?.(firstSession);
await firstCreate;
const sessionIDs = useTerminalStore.getState().workspace?.sessions.map((session) => session.id).sort();
if (JSON.stringify(sessionIDs) !== JSON.stringify(["first", "second"])) {
  throw new Error(`concurrent creates lost a session: ${JSON.stringify(sessionIDs)}`);
}
process.stdout.write("PASS concurrent terminal creates merge into the latest workspace state\n");

if (previousWindow) globalThis.window = previousWindow;
else delete (globalThis as { window?: unknown }).window;
