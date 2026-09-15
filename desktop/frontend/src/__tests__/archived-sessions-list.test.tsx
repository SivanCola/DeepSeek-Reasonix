import assert from "node:assert/strict";
import { managementDom } from "../test-support/managementDom";
import { installDesktopHostStub } from "./desktopHostStub";
const dom = managementDom();
const { default: React, act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { LocaleProvider } = await import("../lib/i18n");
const { ToastProvider } = await import("../lib/toast");
const { ArchivedSessionsList } = await import("../components/ArchivedSessionsList");
const row = (id: string, archived: boolean) => ({ ref: { hostId: "local", sessionId: id }, title: id, preview: "", archived, metadataStatus: "ready" });
const calls: string[] = [];
let restored = false;
let resolveLate: ((value: unknown) => void) | undefined;
const host = installDesktopHostStub({
  GetWorkspaceSnapshot: async () => ({ workspaces: [{ id: "hidden", title: "Hidden project", visible: false }] }),
  ListWorkspaceSessions: async (id: string, query: string, cursor: string, _limit: number, includeArchived: boolean) => {
    assert.equal(includeArchived, true);
    calls.push(`${id}:${query}:${cursor}`);
    if (query === "late") return new Promise(resolve => { resolveLate = resolve; });
    return cursor ? { sessions: restored ? [] : [row("archived", true)], nextCursor: "" }
      : { sessions: [row("ordinary", false)], nextCursor: "page-2" };
  },
  RestoreCanonicalSession: async (ref: { sessionId: string }) => { assert.equal(ref.sessionId, "archived"); restored = true; },
});
const opened: string[] = [];
const root = createRoot(document.getElementById("root")!);
const render = (active = true) => <LocaleProvider><ToastProvider><ArchivedSessionsList active={active} onOpenSession={async ref => { opened.push(ref.sessionId); }} /></ToastProvider></LocaleProvider>;
await act(async () => root.render(render()));
assert.equal(document.querySelectorAll(".archived-sessions__row").length, 1, "archives beyond the first page stay reachable in hidden projects");
assert.ok(calls.includes("hidden::page-2"));
assert.ok(document.body.textContent?.includes("Hidden project"));
await act(async () => (document.querySelector(".archived-sessions__open") as HTMLButtonElement).click());
assert.deepEqual(opened, ["archived"], "archived opens route through the shared navigation owner");
await act(async () => (document.querySelector('[aria-label="Restore session"]') as HTMLButtonElement).click());
assert.equal(document.querySelectorAll(".archived-sessions__row").length, 0);
assert.ok(document.body.textContent?.includes("No archived sessions"));
await act(async () => {
  const input = document.querySelector("input")!;
  Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value")!.set!.call(input, "late");
  input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
});
assert.ok(resolveLate, "query starts a pending read");
await act(async () => root.render(render(false)));
await act(async () => resolveLate!({ sessions: [row("stale", true)], nextCursor: "" }));
assert.equal(document.querySelectorAll(".archived-sessions__row").length, 0, "hidden page rejects stale reads");
await act(async () => root.unmount());
host.uninstall();
dom.window.close();
console.log("PASS archived pagination, hidden workspace, navigation, restore and stale-load isolation");
