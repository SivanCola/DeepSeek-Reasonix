#!/usr/bin/env node

import { appendFile, readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

function invariant(condition, message) {
  if (!condition) throw new Error(message);
}

export function verifyReleaseCI(runs, sha) {
  invariant(/^[0-9a-f]{40}$/.test(sha), "candidate SHA must be a full commit SHA");
  invariant(Array.isArray(runs), "CI run input must be an array");
  const matching = runs
    .filter((run) => run.headSha === sha && run.headBranch === "main-v2" && run.event === "push")
    .sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt));
  invariant(matching.length > 0, `no main-v2 push CI run exists for ${sha}`);
  const latest = matching[0];
  invariant(latest.status === "completed" && latest.conclusion === "success", `latest CI run for ${sha} is ${latest.status}/${latest.conclusion || "none"}`);
  return latest;
}

async function main() {
  const args = process.argv.slice(2);
  const inputIndex = args.indexOf("--input");
  const shaIndex = args.indexOf("--sha");
  invariant(inputIndex >= 0 && args[inputIndex + 1], "--input is required");
  invariant(shaIndex >= 0 && args[shaIndex + 1], "--sha is required");
  const runs = JSON.parse(await readFile(resolve(args[inputIndex + 1]), "utf8"));
  const run = verifyReleaseCI(runs, args[shaIndex + 1]);
  if (process.env.GITHUB_OUTPUT) {
    await appendFile(process.env.GITHUB_OUTPUT, `ci_run_id=${run.databaseId}\nci_run_url=${run.url}\n`);
  }
  console.log(`Release CI verified: ${run.url || run.databaseId}`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    console.error(`verify-release-ci: ${error.message}`);
    process.exitCode = 1;
  });
}
