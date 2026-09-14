import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { installDesktopHostStub } from "./desktopHostStub";
import { performResourceAction } from "../lib/fileNavigationCommands";
import { createFileNavigationOwner, setFileNavigationOwner } from "../lib/fileNavigationCommands";
import { fileNavigationKey, type FileNavigationSnapshot } from "../lib/fileNavigationOwner";
import { useActivityBarStore } from "../store/activityBar";
import { useBrowserPanelStore } from "../lib/browserPanelStore";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
const dom = new JSDOM("", { url: "http://localhost" });
Object.assign(globalThis, { window: dom.window, document: dom.window.document });
const paths = new Map<string, ReturnType<typeof deferred<string>>>();
const revoked: string[] = [];
const stub = installDesktopHostStub({
  ResolveRemoteWorkspacePathForTab: (_tab: string, _host: string, _tool: string, path: string) => {
    const pending = deferred<string>(); paths.set(path, pending); return pending.promise;
  },
  CreatePresentedBrowserPreviewForTab: async () => "http://preview.test/one",
  RevokeWorkspaceBrowserPreview: async (url: string) => { revoked.push(url); },
});
const owner = createFileNavigationOwner();
setFileNavigationOwner(owner);
// A remote reference targets the remote dock; a local one targets the file dock.
const dockTabId = useActivityBarStore.getState().openEntry("remote", "Remote");
const fileDockTabId = useActivityBarStore.getState().openEntry("file", "Files");
const ref = { hostId: "remote", tabId: "session", source: "workspace" as const, toolCallId: "tool" };
const snapshot = (): FileNavigationSnapshot | null =>
  owner.getSnapshot(fileNavigationKey({ sessionTabId: "session", dockTabId }));

// A resolves after B: the late result of the superseded command must not win.
const first = performResourceAction({ ...ref, path: "first" }, "preview");
const second = performResourceAction({ ...ref, path: "second" }, "source");
paths.get("second")!.resolve("/second"); await second;
const current = snapshot();
paths.get("first")!.resolve("/first"); await first;
assert.equal(snapshot(), current, "a superseded resolution must not produce a new snapshot");
assert.equal(current?.selected?.resource.path, "/second");
assert.equal(current?.selected?.source, true);

// Closing the target dock ends the record: a late rejection is a cancellation,
// not a failure reported back to the row that asked.
const cancelled = performResourceAction({ ...ref, path: "cancelled" }, "preview");
owner.retain([fileNavigationKey({ sessionTabId: "session", dockTabId: fileDockTabId })]);
paths.get("cancelled")!.reject(new Error("obsolete failure"));
assert.deepEqual(await cancelled, { status: "cancelled", reason: "superseded" });
assert.equal(snapshot(), null, "a closed dock keeps no record to restore");

// A resolution that lands after its session was replaced is a cancellation too:
// the record it belonged to is gone, and the new session's dock reads nothing.
const switched = performResourceAction({ ...ref, path: "switched" }, "preview");
owner.retain([fileNavigationKey({ sessionTabId: "session", dockTabId: fileDockTabId })]);
paths.get("switched")!.resolve("/switched");
assert.deepEqual(await switched, { status: "cancelled", reason: "superseded" });
assert.equal(snapshot(), null, "a session switch leaves no record behind");

const opened = deferred<{ id: string }>();
const opening = deferred<void>();
const closed: string[] = [];
const host = {
  open: () => { opening.resolve(); return opened.promise; },
  close: async (id: string) => { closed.push(id); },
};
useBrowserPanelStore.setState({ host: host as unknown as NonNullable<ReturnType<typeof useBrowserPanelStore.getState>["host"]> });
const browser = performResourceAction({ hostId: "local", tabId: "session", source: "presented", toolCallId: "tool", path: "one.html" }, "browser");
await opening.promise;
owner.retain([]);
opened.resolve({ id: "only-owned-tab" });
assert.deepEqual(await browser, { status: "cancelled", reason: "superseded" });
assert.deepEqual(revoked, ["http://preview.test/one"], "a URL created after its dock closed is revoked once");
assert(!useBrowserPanelStore.getState().tabs.some(tab => tab.id === "only-owned-tab"));
assert.deepEqual(closed, ["only-owned-tab"]);

// A host that never arrives must not open a page, and its URL is released.
useBrowserPanelStore.setState({ host: null });
const waiting = performResourceAction({ hostId: "local", tabId: "session", source: "presented", toolCallId: "tool", path: "two.html" }, "browser");
await new Promise((resolve) => setTimeout(resolve, 2200));
assert.deepEqual(await waiting, { status: "failed", error: new Error("Built-in browser is not ready") });
assert.deepEqual(revoked, ["http://preview.test/one", "http://preview.test/one"], "a preview whose host never arrived is released");
assert.equal(useBrowserPanelStore.getState().tabs.length, 0, "no page opens without a host");
stub.uninstall(); dom.window.close();
console.log("PASS navigation ordering, dock-scoped cancellation, obsolete errors and browser resource cleanup");
