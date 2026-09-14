import assert from "node:assert/strict";
import test from "node:test";
import { collectTranscriptPerformance, decideTranscriptPerformance, formatPerformanceSummary, needsBoundedRetry, TRANSCRIPT_PHASES } from "./transcript-performance.mjs";

function attempt(ordinal, overrides = {}) {
  const maxima = overrides.maxima ?? {};
  return {
    attempt: ordinal,
    inputCount: overrides.inputCount ?? 30,
    inputP95: overrides.inputP95 ?? 80,
    longTaskSupported: true,
    errors: overrides.errors ?? [],
    phases: Object.fromEntries(TRANSCRIPT_PHASES.map(phase => [phase, {
      elapsedMs: 100,
      longTasks: [],
      longTaskMax: maxima[phase] ?? 100,
    }])),
  };
}

test("a passing first attempt is not retried", async () => {
  const first = attempt(1);
  assert.equal(needsBoundedRetry(first), false);
  assert.equal(decideTranscriptPerformance([first]).status, "passed-first-attempt");
  const calls = [];
  const result = await collectTranscriptPerformance(async ordinal => { calls.push(ordinal); return attempt(ordinal); });
  assert.deepEqual(calls, [1]);
  assert.equal(result.decision.status, "passed-first-attempt");
});

test("558/420/430 passes after bounded retry and keeps the first sample", () => {
  const attempts = [attempt(1, { maxima: { historyPaging: 558 } }), attempt(2, { maxima: { historyPaging: 420 } }), attempt(3, { maxima: { historyPaging: 430 } })];
  const decision = decideTranscriptPerformance(attempts);
  assert.equal(decision.status, "passed-after-bounded-retry");
  assert.equal(decision.medians.historyPaging, 430);
  assert.equal(attempts[0].phases.historyPaging.longTaskMax, 558);
  assert.match(formatPerformanceSummary("chromium", 1000, decision, attempts), /#1 \[.*historyPaging=558ms.*#3 \[.*historyPaging=430ms/);
});

test("558/520/430 remains a sustained failure", () => {
  const decision = decideTranscriptPerformance([
    attempt(1, { maxima: { streaming: 558 } }),
    attempt(2, { maxima: { streaming: 520 } }),
    attempt(3, { maxima: { streaming: 430 } }),
  ]);
  assert.equal(decision.status, "failed-sustained-regression");
  assert.equal(decision.medians.streaming, 520);
});

test("phases are judged independently", () => {
  const decision = decideTranscriptPerformance([
    attempt(1, { maxima: { initialLoad: 530, input: 540 } }),
    attempt(2, { maxima: { initialLoad: 400, input: 550 } }),
    attempt(3, { maxima: { initialLoad: 420, input: 410 } }),
  ]);
  assert.equal(decision.medians.initialLoad, 420);
  assert.equal(decision.medians.input, 540);
  assert.equal(decision.passed, false);
});

test("functional errors, strict input regressions, and missing samples fail immediately", () => {
  assert.throws(() => needsBoundedRetry(attempt(1, { errors: ["page error"] })), /browser errors/);
  assert.throws(() => needsBoundedRetry(attempt(1, { inputP95: 201 })), /input P95/);
  const incomplete = attempt(1);
  delete incomplete.phases.streaming;
  assert.throws(() => needsBoundedRetry(incomplete), /missing streaming/);
  assert.throws(() => decideTranscriptPerformance([attempt(1, { maxima: { historyPaging: 558 } })]), /exactly two retries/);
});

test("bounded collection makes exactly two retries after a long-task exceedance", async () => {
  const calls = [];
  const result = await collectTranscriptPerformance(async ordinal => {
    calls.push(ordinal);
    return attempt(ordinal, { maxima: { historyPaging: ordinal === 1 ? 558 : 420 } });
  });
  assert.deepEqual(calls, [1, 2, 3]);
  assert.equal(result.decision.status, "passed-after-bounded-retry");
  const functionalCalls = [];
  await assert.rejects(() => collectTranscriptPerformance(async ordinal => {
    functionalCalls.push(ordinal);
    throw new Error("functional failure");
  }), /functional failure/);
  assert.deepEqual(functionalCalls, [1]);
});
