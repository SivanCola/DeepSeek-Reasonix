#!/usr/bin/env tsx
import { renderCacheGuardReport, runCacheGuard } from "../src/telemetry/cache-guard.js";

function readFlag(name: string): string | null {
  const prefix = `--${name}=`;
  const match = process.argv.slice(2).find((arg) => arg.startsWith(prefix));
  return match ? match.slice(prefix.length) : null;
}

const json = process.argv.includes("--json");
const keepTemp = process.argv.includes("--keep-temp");
const thresholdRaw = readFlag("threshold");
const threshold = thresholdRaw === null ? undefined : Number(thresholdRaw);

if (threshold !== undefined && (!Number.isFinite(threshold) || threshold <= 0 || threshold > 1)) {
  process.stderr.write("--threshold must be a number in (0, 1]. Example: --threshold=0.92\n");
  process.exit(2);
}

const report = await runCacheGuard({ minHitRatio: threshold, keepTemp });
if (json) {
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
} else {
  process.stdout.write(`${renderCacheGuardReport(report)}\n`);
}
process.exitCode = report.passed ? 0 : 1;
