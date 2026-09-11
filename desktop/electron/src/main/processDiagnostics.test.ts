import assert from "node:assert/strict";
import type { ProcessMetric } from "electron";
import { test } from "node:test";
import { ProcessDiagnostics } from "./processDiagnostics.js";

test("samples are bounded, redact metadata and preserve unavailable fields", () => {
  let now = 0;
  let calls = 0;
  const sampler = new ProcessDiagnostics(() => {
    calls++;
    return [{ pid: 42, type: "Tab", name: "private title", cpu: { percentCPUUsage: 2 }, memory: { workingSetSize: 2048 } }] as ProcessMetric[];
  }, () => now);
  const first = sampler.snapshot();
  assert.deepEqual(first.samples[0].processes, [{ pid: 42, type: "Tab", cpuPercent: null, workingSetMb: 2, privateMb: null }]);
  sampler.snapshot();
  assert.equal(calls, 1);
  for (let i = 1; i <= 20; i++) { now = i * 5000; sampler.sample(); }
  const snapshot = sampler.snapshot();
  assert.equal(snapshot.samples.length, 12);
  assert.equal(snapshot.samples.at(-1)?.intervalMs, 5000);
  assert.equal(snapshot.samples.at(-1)?.processes[0].cpuPercent, 2);
  assert.equal(JSON.stringify(snapshot).includes("private title"), false);
});

test("unavailable and expired samples do not become zero readings", () => {
  let now = 0;
  let fail = false;
  const sampler = new ProcessDiagnostics(() => { if (fail) throw Error("gone"); return []; }, () => now);
  sampler.sample();
  fail = true;
  now = 61000;
  assert.deepEqual(sampler.snapshot().samples, []);
});
