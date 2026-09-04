import { TranscriptMeasurementLedger } from "../lib/transcriptMeasurementLedger";

let passed = 0;
let failed = 0;
function ok(condition: unknown, label: string) {
  if (condition) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

console.log("\nTranscript immutable measurement ledger");

const ledger = new TranscriptMeasurementLedger();
ok(!ledger.commit([]), "an empty measurement batch is a no-op");

ok(ledger.commit([
  { key: "turn:1", size: 120 },
  { key: "turn:2", size: 240 },
]), "a valid measurement batch commits");
ok(ledger.sizeFor("turn:1", 64) === 120 && ledger.sizeFor("turn:2", 64) === 240, "all measurements become visible in the same snapshot");

ok(!ledger.commit([
  { key: "turn:1", size: 120.2 },
  { key: "turn:invalid", size: Number.NaN },
]), "sub-pixel noise and invalid measurements do not publish a partial snapshot");
ok(ledger.sizeFor("turn:invalid", 64) === 64, "ignored measurements leave the prior snapshot authoritative");

ok(ledger.commit([
  { key: "turn:1", size: 140 },
  { key: "turn:3", size: 360 },
]), "a later atomic batch replaces every changed key together");
ok(ledger.sizeFor("turn:1", 64) === 140 && ledger.sizeFor("turn:3", 64) === 360, "the second batch publishes complete contents");

ok(ledger.retain(new Set(["turn:1", "turn:3"])), "retaining live block identities removes obsolete measurements");
ok(ledger.sizeFor("turn:2", 64) === 64, "retention publishes one pruned snapshot");
ok(!ledger.retain(new Set(["turn:1", "turn:3"])), "retaining an unchanged identity set is a no-op");

console.log(`\n${passed} passed, ${failed} failed`);
if (failed) process.exit(1);
