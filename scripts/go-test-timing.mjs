#!/usr/bin/env node

import { appendFileSync } from "node:fs";
import { createInterface } from "node:readline";

function args(argv) {
  const values = {};
  for (let index = 0; index < argv.length; index += 2) {
    if (!argv[index]?.startsWith("--") || argv[index + 1] === undefined) throw new Error(`invalid argument ${argv[index] ?? ""}`);
    values[argv[index].slice(2).replaceAll("-", "_")] = argv[index + 1];
  }
  return values;
}

export function timingReport(events, { label, startedMs, completedMs }) {
  const packageStarts = events.filter(event => event.Action === "start" && event.Package && event.Time)
    .map(event => Date.parse(event.Time)).filter(Number.isFinite);
  const packages = events.filter(event => ["pass", "fail", "skip"].includes(event.Action) && event.Package && !event.Test);
  const elapsed = packages.map(event => Number(event.Elapsed)).filter(Number.isFinite);
  const failed = packages.filter(event => event.Action === "fail").length;
  const seconds = value => (value / 1_000).toFixed(1);
  const firstStart = packageStarts.length ? Math.min(...packageStarts) : undefined;
  const lastStart = packageStarts.length ? Math.max(...packageStarts) : undefined;
  const slowest = packages.filter(event => Number.isFinite(Number(event.Elapsed)))
    .sort((left, right) => Number(right.Elapsed) - Number(left.Elapsed)).slice(0, 5);
  return [
    `### ${label} Go test timing`, "",
    `- Wall: ${seconds(completedMs - startedMs)}s`,
    `- First package start: ${firstStart === undefined ? "unavailable" : `${seconds(firstStart - startedMs)}s`}`,
    `- Last package start: ${lastStart === undefined ? "unavailable" : `${seconds(lastStart - startedMs)}s`}`,
    `- Package elapsed sum: ${elapsed.reduce((sum, value) => sum + value, 0).toFixed(1)}s across ${packages.length} packages (${failed} failed)`,
    "- Slowest packages: " + (slowest.length ? slowest.map(event => `${event.Package} ${Number(event.Elapsed).toFixed(1)}s`).join(", ") : "unavailable"),
    "",
  ].join("\n");
}

async function main() {
  const options = args(process.argv.slice(2));
  const startedMs = Number(options.started_ms);
  if (!options.label || !Number.isFinite(startedMs)) throw new Error("--label and numeric --started-ms are required");
  const events = [];
  for await (const line of createInterface({ input: process.stdin, crlfDelay: Infinity })) {
    if (!line) continue;
    const event = JSON.parse(line);
    if ((event.Action === "start" && event.Package) ||
        (["pass", "fail", "skip"].includes(event.Action) && event.Package && !event.Test)) events.push(event);
    if (typeof event.Output === "string") process.stdout.write(event.Output);
  }
  const report = timingReport(events, { label: options.label, startedMs, completedMs: Date.now() });
  process.stdout.write(`\n${report}`);
  if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, report);
}

if (process.argv[1] && import.meta.url === new URL(`file://${process.argv[1]}`).href) {
  main().catch(error => {
    console.error(`go-test-timing: ${error.message}`);
    process.exitCode = 1;
  });
}
