#!/usr/bin/env node

import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

function invariant(condition, message) {
  if (!condition) throw new Error(message);
}

export function verifyPreviewSoak(event, { now = Date.now(), minimumHours = 24, allowEarly = false } = {}) {
  invariant(event?.channel === "preview", "source release event must be Preview");
  const publishedAt = Date.parse(event.publishedAt);
  invariant(Number.isFinite(publishedAt), "source Preview publishedAt must be a valid ISO timestamp");
  invariant(publishedAt <= now, "source Preview publishedAt cannot be in the future");
  const ageMs = now - publishedAt;
  const minimumMs = minimumHours * 60 * 60 * 1000;
  invariant(allowEarly || ageMs >= minimumMs, `source Preview is only ${(ageMs / 3_600_000).toFixed(2)}h old; ${minimumHours}h is required`);
  return { publishedAt: event.publishedAt, ageHours: ageMs / 3_600_000 };
}

async function main() {
  const args = process.argv.slice(2);
  const inputIndex = args.indexOf("--input");
  invariant(inputIndex >= 0 && args[inputIndex + 1], "--input is required");
  const hoursIndex = args.indexOf("--minimum-hours");
  const minimumHours = hoursIndex >= 0 ? Number(args[hoursIndex + 1]) : 24;
  invariant(Number.isFinite(minimumHours) && minimumHours >= 0, "--minimum-hours must be non-negative");
  const event = JSON.parse(await readFile(resolve(args[inputIndex + 1]), "utf8"));
  const result = verifyPreviewSoak(event, {
    minimumHours,
    allowEarly: args.includes("--allow-early"),
    now: process.env.NOW_EPOCH_MS ? Number(process.env.NOW_EPOCH_MS) : Date.now(),
  });
  if (process.env.GITHUB_OUTPUT) {
    const { appendFile } = await import("node:fs/promises");
    await appendFile(process.env.GITHUB_OUTPUT, `source_published_at=${result.publishedAt}\npreview_age_hours=${result.ageHours.toFixed(2)}\n`);
  }
  console.log(`Preview soak verified: ${result.ageHours.toFixed(2)}h${args.includes("--allow-early") ? " (emergency override)" : ""}`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    console.error(`verify-preview-soak: ${error.message}`);
    process.exitCode = 1;
  });
}
