// Run: tsx src/__tests__/file-navigation-owner.test.ts
//
// The navigation instance is exercised in isolation: ports are plain functions,
// so every assertion is about identity, revision and lifetime rather than about
// any store, bridge or React runtime.

import assert from "node:assert/strict";
import { fileAccessContext, type FileResourceRef } from "../lib/fileResource";
import { FILE_PREVIEW_LIMIT, FileNavigationOwner, fileNavigationKey } from "../lib/fileNavigationOwner";

const DOCK = "dock-file";
const scope = { sessionTabId: "session-a", dockTabId: DOCK };
const key = fileNavigationKey(scope);
const owner = new FileNavigationOwner({
  resolve: (ref) => ({ hostId: ref.hostId, path: ref.path, requestedPath: ref.path, access: fileAccessContext(ref) }),
  revealDock: () => DOCK,
});
const presented = (path: string, toolCallId = "call"): FileResourceRef =>
  ({ source: "presented", hostId: "local", tabId: "session-a", toolCallId, path });
const workspace = (path: string): FileResourceRef =>
  ({ source: "workspace", hostId: "local", tabId: "session-a", path });
const snapshot = () => owner.getSnapshot(key);

// ── Identity: the same resource and mode is one entry, one revision ──
owner.open({ ref: presented("app.ts"), params: { action: "preview", view: "files" } });
const opened = snapshot()!;
assert.equal(opened.selected?.resource.path, "app.ts");
assert.equal(opened.entries.length, 1);
assert.equal(opened.sourcePaths.length, 0, "a preview entry is not a source entry");
const contentRevision = opened.contentRevision;
owner.open({ ref: presented("app.ts"), params: { action: "preview", view: "files" } });
assert.equal(snapshot()!.entries, opened.entries, "an equivalent entry keeps its list identity");
assert.equal(snapshot()!.selected, opened.selected, "an equivalent entry keeps its identity");
assert.equal(snapshot()!.contentRevision, contentRevision, "a repeat does not restart the read inputs");
assert.equal(snapshot()!.navigation!.revision, opened.navigation!.revision + 1, "a repeat is still delivered as its own navigation");

// Switching the mode reuses the tab and changes the read inputs.
owner.setSourceMode(scope, "app.ts", true);
assert.equal(snapshot()!.entries.length, 1, "switching to source reuses the preview tab");
assert.deepEqual(snapshot()!.sourcePaths, ["app.ts"]);
assert.notEqual(snapshot()!.contentRevision, contentRevision, "switching the mode asks for the other representation");
owner.setSourceMode(scope, "app.ts", false);
assert.deepEqual(snapshot()!.sourcePaths, [], "switching back clears the source mode");

// The same path from another entry point is the same resource, new credentials.
const presentedEntry = snapshot()!.selected;
owner.open({ ref: workspace("app.ts"), params: { action: "preview", view: "files" } });
assert.equal(snapshot()!.entries.length, 1, "one path is one preview tab");
assert.notEqual(snapshot()!.selected, presentedEntry, "a new access context is a new entry record");
assert.deepEqual(snapshot()!.selected!.resource.access, { source: "workspace", tabId: "session-a" });
assert.equal(snapshot()!.selected!.resource.access.toolCallId, undefined, "a workspace reference never inherits a presented tool scope");

// ── Tabs: recency, the cap, and the neighbour a close selects ──
for (const path of ["b.ts", "c.ts", "d.ts", "e.ts", "f.ts"]) {
  owner.open({ ref: workspace(path), params: { action: "preview", view: "files" } });
}
assert.equal(snapshot()!.entries.length, FILE_PREVIEW_LIMIT, "the preview tab list keeps its limit");
assert.deepEqual(snapshot()!.entries.map((entry) => entry.resource.path), ["b.ts", "c.ts", "d.ts", "e.ts", "f.ts"]);
owner.open({ ref: workspace("c.ts"), params: { action: "preview", view: "files" } });
assert.deepEqual(snapshot()!.entries.map((entry) => entry.resource.path), ["b.ts", "d.ts", "e.ts", "f.ts", "c.ts"], "reopening moves the entry to the most recent position");
owner.closeEntry(scope, "c.ts");
assert.deepEqual(snapshot()!.entries.map((entry) => entry.resource.path), ["b.ts", "d.ts", "e.ts", "f.ts"]);
assert.equal(snapshot()!.selected?.resource.path, "f.ts", "closing the selected tab selects the previous one");
owner.selectEntry(scope, "b.ts");
assert.equal(snapshot()!.selected?.resource.path, "b.ts");
owner.closeEntry(scope, "b.ts");
assert.equal(snapshot()!.selected?.resource.path, "f.ts", "closing an unselected tab keeps the selection");
const beforeUnknownClose = snapshot();
owner.closeEntry(scope, "missing.ts");
assert.equal(snapshot(), beforeUnknownClose, "closing a tab that is not open changes nothing");

// ── A scoped list drops the tabs but keeps the selection ──
owner.clearEntries(scope);
assert.deepEqual(snapshot()!.entries, []);
assert.equal(snapshot()!.selected?.resource.path, "f.ts", "a scoped list does not close the preview");
owner.clearSelection(scope);
assert.equal(snapshot()!.selected, null);
const cleared = snapshot();
owner.clearSelection(scope);
assert.equal(snapshot(), cleared, "clearing an empty selection produces no new snapshot");

// ── Restore carries workspace access only ──
owner.dispose();
const restoredOwner = new FileNavigationOwner({
  resolve: (ref) => ({ hostId: ref.hostId, path: ref.path, requestedPath: ref.path, access: fileAccessContext(ref) }),
  revealDock: () => DOCK,
});
restoredOwner.bindScope(scope, "workspace-scope");
restoredOwner.restore(scope, { paths: ["a.md", "b.md"], selectedPath: "b.md", hostId: "local" });
const restored = restoredOwner.getSnapshot(key)!;
assert.deepEqual(restored.entries.map((entry) => entry.resource.path), ["a.md", "b.md"]);
assert.equal(restored.selected?.resource.path, "b.md");
assert.deepEqual(restored.selected?.resource.access, { source: "workspace", tabId: "session-a" }, "a remembered path never restores a presented scope");
assert.equal(restored.navigation, null, "a restore is not a command and carries no navigation intent");
const restoredSnapshot = restoredOwner.getSnapshot(key);
restoredOwner.restore(scope, { paths: ["c.md"], selectedPath: null, hostId: "local" });
assert.equal(restoredOwner.getSnapshot(key), restoredSnapshot, "a restore never outranks a record a command or an earlier restore wrote");

// ── Lifetime: a scope change and a closed dock end the record ──
const scopeSnapshot = restoredOwner.getSnapshot(key)!;
restoredOwner.bindScope(scope, "another-workspace-scope");
const rescoped = restoredOwner.getSnapshot(key)!;
assert.notEqual(rescoped, scopeSnapshot);
assert(scopeSnapshot.signal.aborted, "a different workspace scope ends the previous lifetime");
assert.deepEqual(rescoped.entries, [], "a different workspace scope keeps no entries");
assert(rescoped.generation > scopeSnapshot.generation, "a rebuilt record advances its generation");
restoredOwner.bindScope(scope, "another-workspace-scope");
assert.equal(restoredOwner.getSnapshot(key), rescoped, "binding the same scope again is not a new lifetime");

restoredOwner.retain([fileNavigationKey({ sessionTabId: "session-a", dockTabId: "other-dock" })]);
assert.equal(restoredOwner.getSnapshot(key), null, "a dock that is no longer open keeps no record");
assert(rescoped.signal.aborted);
restoredOwner.bindScope(scope, "another-workspace-scope");
const reopened = restoredOwner.getSnapshot(key)!;
assert(reopened.generation > rescoped.generation, "reopening the same dock tab id starts a new lifecycle generation");
assert.deepEqual(reopened.entries, [], "a lifecycle generation never restores the previous one's previews");
restoredOwner.open({ ref: workspace("b.md"), params: { action: "preview", view: "files" } });

// ── Isolation: another session, another host, another dock are other records ──
const otherSession = fileNavigationKey({ sessionTabId: "session-b", dockTabId: DOCK });
const otherDock = fileNavigationKey({ sessionTabId: "session-a", dockTabId: "dock-remote" });
assert.equal(restoredOwner.getSnapshot(otherSession), null);
assert.equal(restoredOwner.getSnapshot(otherDock), null);
restoredOwner.retain([key, otherSession]);
restoredOwner.open({ ref: { source: "presented", hostId: "local", tabId: "session-b", toolCallId: "call", path: "other.ts" }, params: { action: "preview", view: "files" } });
assert.equal(restoredOwner.getSnapshot(key)!.selected?.resource.path, "b.md", "another session's command leaves this dock alone");
assert.equal(restoredOwner.getSnapshot(otherSession)!.selected?.resource.path, "other.ts");

// ── A panel acting on its own contents never picks a dock ──
let reveals = 0;
const panelOwner = new FileNavigationOwner({
  resolve: (ref) => ({ hostId: ref.hostId, path: ref.path, requestedPath: ref.path, access: fileAccessContext(ref) }),
  revealDock: () => { reveals += 1; return "another-dock"; },
});
panelOwner.openIn(scope, { ref: workspace("own.ts"), params: { action: "preview", view: "files" } });
assert.equal(reveals, 0, "a command inside a panel must not ask which dock to open");
assert.equal(panelOwner.getSnapshot(key)!.selected?.resource.path, "own.ts", "it commits to the dock the caller named");
assert.equal(panelOwner.getSnapshot(fileNavigationKey({ sessionTabId: "session-a", dockTabId: "another-dock" })), null);

// ── A failed resolution reports to its caller and commits nothing ──
const failing = new FileNavigationOwner({
  resolve: () => { throw new Error("path not permitted"); },
  revealDock: () => DOCK,
});
assert.deepEqual(failing.open({ ref: workspace("secret.ts"), params: { action: "preview", view: "files" } }),
  { status: "failed", error: new Error("path not permitted") });
assert.equal(failing.getSnapshot(key)?.selected ?? null, null);

restoredOwner.dispose();
console.log("PASS file navigation identity, revisions, tabs, restore and lifetimes");
