import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { installDesktopHostStub } from "./desktopHostStub";
import { useConfigDiagnostics } from "../lib/useConfigDiagnostics";
import { diagnosticDismissKey, normalizeConfigDiagnostics, type ConfigDiagnosticSnapshot } from "../lib/configDiagnostics";

const dom = new JSDOM('<div id="root"></div>', { url: "http://localhost" });
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const calls: { tab: string; resolve: (snapshot: ConfigDiagnosticSnapshot) => void }[] = [];
const stub = installDesktopHostStub({ ConfigDiagnostics: (tab: string) => new Promise<ConfigDiagnosticSnapshot>(resolve => calls.push({ tab, resolve })) });
let state!: ReturnType<typeof useConfigDiagnostics>;
function Probe({ tab }: { tab: string }) { state = useConfigDiagnostics(tab, tab); return <p>{state.warnings.map(i => i.id).join(",")}</p>; }
function snapshot(root: string, revision: number, problem = ""): ConfigDiagnosticSnapshot {
  return { hostId: "test-host", workspaceId: root, workspaceRoot: root, instanceId: "instance-a", revision, status: "ready",
    items: problem ? [{ id: problem, code: "project_config_invalid", scope: "project", source: root+"/reasonix.toml", field: "", severity: "warning", status: "needs_attention", summary: "Invalid project", action: "open_config", count: 1 }] : [] };
}
const root = createRoot(document.getElementById("root")!);
await act(async () => root.render(<Probe tab="a" />));
const a = calls.at(-1)!;
await act(async () => root.render(<Probe tab="b" />));
const b = calls.at(-1)!;
await act(async () => b.resolve(snapshot("b", 2)));
await act(async () => a.resolve(snapshot("a", 99, "late-a")));
assert.equal(state.snapshot?.workspaceId, "b", "late project response cannot replace active project");
assert.equal(state.warnings.length, 0);

await act(async () => { void state.reload(); });
const older = calls.at(-1)!;
await act(async () => { void state.reload(); });
const newer = calls.at(-1)!;
await act(async () => newer.resolve(snapshot("b", 4, "broken-b")));
await act(async () => older.resolve(snapshot("b", 3)));
assert.equal(state.warnings[0]?.id, "broken-b", "older empty response cannot clear a new issue");
await act(async () => state.dismiss("broken-b"));
await act(async () => { void state.reload(); });
await act(async () => calls.at(-1)!.resolve(snapshot("b", 5, "broken-b")));
assert.equal(state.warnings.length, 0, "repeated issue remains dismissed");
await act(async () => { void state.reload(); });
await act(async () => calls.at(-1)!.resolve(snapshot("b", 6, "changed-b")));
assert.equal(state.warnings.length, 1, "changed issue becomes visible");

await act(async () => { void state.reload(); });
const retired = calls.at(-1)!;
await act(async () => stub.emitServiceState({ phase: "ready", generation: "new-service" }));
await act(async () => calls.at(-1)!.resolve({ ...snapshot("b", 1), instanceId: "instance-b" }));
await act(async () => retired.resolve(snapshot("b", 100, "retired-instance")));
assert.equal(state.snapshot?.instanceId, "instance-b", "retired service cannot restore its warning");
assert.equal(state.warnings.length, 0, "empty snapshot clears warnings");
await act(async () => stub.emit("config:diagnostics", snapshot("a", 999, "background-project")));
await act(async () => stub.emit("config:diagnostics", snapshot("b", 999, "old-instance")));
assert.equal(state.warnings.length, 0, "background project and retired service events are ignored");
await act(async () => stub.emit("config:diagnostics", { ...snapshot("b", 2, "new-event"), instanceId: "instance-b" }));
assert.equal(state.warnings[0]?.id, "new-event");
await act(async () => stub.emit("config:diagnostics", { ...snapshot("b", 3), instanceId: "instance-b" }));
assert.equal(state.warnings.length, 0, "empty event clears current project");

await act(async () => stub.emit("config:load-warnings", ["unscoped warning from another project"], 900));
assert.equal(state.warnings.length, 0, "legacy event is an invalidation, not project data");
await act(async () => calls.at(-1)!.resolve({ ...snapshot("b", 4), status: "unsupported", instanceId: "instance-b" }));
assert.equal(state.snapshot?.status, "unsupported");
assert.deepEqual(normalizeConfigDiagnostics({ ...snapshot("x", 1), items: null })?.items, []);
assert.equal(normalizeConfigDiagnostics({}), null);
assert.equal(normalizeConfigDiagnostics({ ...snapshot("x", 1), items: [{ unknown: "future" }] }), null, "unknown item cannot be reported as healthy");
const globalA = snapshot("a", 1, "global-error");
globalA.items[0].scope = "user";
assert.equal(diagnosticDismissKey(globalA, globalA.items[0]), diagnosticDismissKey({ ...globalA, workspaceId: "b" }, globalA.items[0]), "global issue belongs to host");
assert.notEqual(diagnosticDismissKey(globalA, globalA.items[0]), diagnosticDismissKey({ ...globalA, hostId: "another-host" }, globalA.items[0]), "global issue does not cross hosts");
await act(async () => stub.emitServiceState({ phase: "failed", generation: "new-service" }));
assert.equal(state.loading, false);
assert.equal(state.unavailable, true, "failed service is unavailable, not perpetually loading or healthy");

await act(async () => { void state.reload(); });
const closed = calls.at(-1)!;
await act(async () => root.unmount());
await act(async () => closed.resolve(snapshot("b", 200, "closed-tab")));
assert.equal(stub.events.get("config:load-warnings")?.size ?? 0, 0);
stub.uninstall();
console.log("PASS configuration diagnostics binding, ordering, service restart, dismissal and empty-state contracts");
