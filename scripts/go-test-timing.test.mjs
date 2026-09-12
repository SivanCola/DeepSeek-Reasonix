import assert from "node:assert/strict";
import test from "node:test";
import { timingReport } from "./go-test-timing.mjs";

test("reports package launch, wall, failure and slow-package timing", () => {
  const events = [
    { Time: "2026-01-01T00:00:02.000Z", Action: "start", Package: "example/fast" },
    { Time: "2026-01-01T00:00:04.000Z", Action: "start", Package: "example/slow" },
    { Time: "2026-01-01T00:00:05.000Z", Action: "pass", Package: "example/fast", Elapsed: 3 },
    { Time: "2026-01-01T00:00:10.000Z", Action: "fail", Package: "example/slow", Elapsed: 6 },
    { Time: "2026-01-01T00:00:10.000Z", Action: "pass", Package: "example/slow", Test: "TestChild", Elapsed: 1 },
  ];
  const report = timingReport(events, {
    label: "Windows desktop",
    startedMs: Date.parse("2026-01-01T00:00:00.000Z"),
    completedMs: Date.parse("2026-01-01T00:00:11.000Z"),
  });
  assert.match(report, /Wall: 11\.0s/);
  assert.match(report, /First package start: 2\.0s/);
  assert.match(report, /Last package start: 4\.0s/);
  assert.match(report, /9\.0s across 2 packages \(1 failed\)/);
  assert.match(report, /example\/slow 6\.0s, example\/fast 3\.0s/);
});

test("reports unavailable phases when compilation fails before a package starts", () => {
  const report = timingReport([], { label: "Windows desktop", startedMs: 1_000, completedMs: 2_500 });
  assert.match(report, /Wall: 1\.5s/);
  assert.match(report, /First package start: unavailable/);
  assert.match(report, /0\.0s across 0 packages/);
});
