import assert from "node:assert/strict";
import { installPerformancePressureMonitor } from "../lib/crash";
import { installDesktopHostStub } from "./desktopHostStub";

const events = new Map<string, () => void>();
let focused = true;
let starts = 0;
let stops = 0;
const staleCallbacks: (() => void)[] = [];
Object.assign(globalThis, {
  window: { addEventListener: (name: string, cb: () => void) => events.set(name, cb), setInterval: () => 1 },
  document: { visibilityState: "visible", hasFocus: () => focused, addEventListener: (name: string, cb: () => void) => events.set(name, cb) },
  Profiler: class {
    constructor() { starts++; }
    async stop() { stops++; return {}; }
    addEventListener(_name: string, cb: () => void) { staleCallbacks.push(cb); }
  },
});
installDesktopHostStub({});
installPerformancePressureMonitor();
assert.equal(starts, 1);
focused = false;
events.get("blur")!();
assert.equal(stops, 1);
staleCallbacks[0]();
assert.equal(starts, 1, "stale full-buffer callback cannot restart a paused sampler");
focused = true;
events.get("focus")!();
events.get("focus")!();
assert.equal(starts, 2, "focus resumes exactly one profiler");
Object.assign(document, { visibilityState: "hidden" });
events.get("visibilitychange")!();
assert.equal(stops, 2);
console.log("profiler lifecycle tests passed");
