#!/usr/bin/env node

import { appendFileSync } from "node:fs";
import { execFile } from "node:child_process";
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

function startWindowsPipeDrainDiagnostics(idleSeconds, startedMs) {
  if (process.platform !== "win32" || !Number.isFinite(idleSeconds) || idleSeconds <= 0) return () => {};
  let lastInputMs = Date.now();
  let snapshotRunning = false;
  const interval = setInterval(() => {
    const idleMs = Date.now() - lastInputMs;
    if (idleMs < idleSeconds * 1_000 || snapshotRunning) return;
    snapshotRunning = true;
    const script = [
      `$started = [DateTimeOffset]::FromUnixTimeMilliseconds(${Math.trunc(startedMs)}).LocalDateTime`,
      "Get-CimInstance Win32_Process |",
      "  Where-Object { $_.CreationDate -ge $started } |",
      "  Sort-Object CreationDate, ProcessId |",
      "  Select-Object ProcessId, ParentProcessId, Name, CreationDate |",
      "  Format-Table -AutoSize | Out-String -Width 200",
    ].join(" ");
    execFile("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", script], { windowsHide: true }, (error, stdout) => {
      snapshotRunning = false;
      process.stderr.write(`\nWindows test output has been idle for ${(idleMs / 1_000).toFixed(0)}s; new process snapshot:\n`);
      process.stderr.write(error ? `snapshot failed: ${error.message}\n` : stdout);
    });
  }, Math.max(1_000, Math.min(idleSeconds * 1_000, 10_000)));
  interval.unref();
  return {
    noteInput() { lastInputMs = Date.now(); },
    stop() { clearInterval(interval); },
  };
}

async function main() {
  const options = args(process.argv.slice(2));
  const startedMs = Number(options.started_ms);
  if (!options.label || !Number.isFinite(startedMs)) throw new Error("--label and numeric --started-ms are required");
  const events = [];
  const diagnostics = startWindowsPipeDrainDiagnostics(Number(options.diagnose_idle_seconds), startedMs);
  for await (const line of createInterface({ input: process.stdin, crlfDelay: Infinity })) {
    if (!line) continue;
    diagnostics.noteInput?.();
    const event = JSON.parse(line);
    if ((event.Action === "start" && event.Package) ||
        (["pass", "fail", "skip"].includes(event.Action) && event.Package && !event.Test)) events.push(event);
    if (typeof event.Output === "string") process.stdout.write(event.Output);
  }
  diagnostics.stop?.();
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
