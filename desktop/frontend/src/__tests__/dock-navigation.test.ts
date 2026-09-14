import assert from "node:assert/strict";
import { DockNavigation, requestKeys, type DockRequests } from "../app-shell/dockNavigation";

for (const key of requestKeys) {
  const navigation = new DockNavigation();
  const request = { id: 1, path: "a", paths: ["a"], changes: [], tabId: "session", summary: {} };
  const incoming = { [key]: request } as DockRequests;
  navigation.commit("project/session", "one", incoming, ["one", "two"]);
  const snapshot = navigation.getSnapshot();
  assert.equal(snapshot[key]?.acceptNavigation?.(), true);
  navigation.commit("project/session", "one", { [key]: { ...request } } as DockRequests, ["one", "two"]);
  assert.equal(navigation.getSnapshot(), snapshot, `${key}: equivalent input must preserve snapshot`);
  assert.equal(snapshot[key]?.acceptNavigation?.(), false);
  navigation.commit("project/session", "one", { [key]: { ...request, navigationSource: "another-producer" } } as DockRequests, ["one", "two"]);
  assert.equal(navigation.getSnapshot()[key]?.acceptNavigation?.(), true, "producer identities cannot collide");
  navigation.commit("project/session", "one", incoming, ["one", "two"]);
  navigation.commit("project/session", "two", incoming, ["one", "two"]);
  assert.equal(navigation.getSnapshot()[key], null);
  navigation.commit("project/session", "one", incoming, ["one", "two"]);
  assert.equal(navigation.getSnapshot()[key], null);
  navigation.commit("project/session", "one", { [key]: { ...request, id: 2 } } as DockRequests, ["one"]);
  const pending = navigation.getSnapshot()[key];
  navigation.commit("project/session", null, {}, []);
  assert.equal(pending?.acceptNavigation?.(), false, "closed occurrence cannot accept a late request");
  navigation.commit("project/session", "one", { [key]: { ...request, id: 2 } } as DockRequests, ["one"]);
  assert.equal(navigation.getSnapshot()[key], null, "reopening same ID does not replay");
  const cancellation = new AbortController();
  navigation.commit("project/session", "one", { [key]: { ...request, id: 3, navigationCancellation: cancellation.signal } } as DockRequests, ["one"]);
  cancellation.abort();
  assert.equal(navigation.getSnapshot()[key]?.acceptNavigation?.(), false, "cancelled navigation cannot be accepted before reconciliation");
  const lifetime = navigation.getSnapshot().navigationSignal!;
  navigation.dispose();
  assert(lifetime.aborted);
}
console.log("PASS five Dock request kinds: stable revision, once-only acceptance, view isolation and close/reopen");
