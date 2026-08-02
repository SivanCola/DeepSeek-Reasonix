#!/usr/bin/env node

import { readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";

const stable = /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/;
const before = process.env.RELEASE_BEFORE || "";
const after = process.env.RELEASE_SHA || "";
const recoveryTag = process.env.RECOVERY_TAG || "";

function catalogAt(ref) {
  if (!ref || /^0+$/.test(ref)) return { releases: [] };
  const text = execFileSync("git", ["show", `${ref}:release-notes/releases.json`], {
    encoding: "utf8",
  });
  return JSON.parse(text);
}

async function emit(values) {
  const output = process.env.GITHUB_OUTPUT;
  if (!output) {
    for (const [key, value] of Object.entries(values)) console.log(`${key}=${value}`);
    return;
  }
  const lines = Object.entries(values).map(([key, value]) => `${key}=${value}`).join("\n");
  const current = readFileSync(output, "utf8");
  await import("node:fs").then(({ writeFileSync }) => writeFileSync(output, `${current}${lines}\n`));
}

let version;
let sha = after;
let recovery = false;
if (recoveryTag) {
  if (!/^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(recoveryTag)) {
    throw new Error("Recover release accepts only vMAJOR.MINOR.PATCH");
  }
  version = recoveryTag.slice(1);
  sha = execFileSync("git", ["rev-list", "-n1", recoveryTag], { encoding: "utf8" }).trim();
  recovery = true;
} else {
  const oldVersions = new Set(catalogAt(before).releases.map((item) => item.version));
  const added = catalogAt(after).releases.filter((item) => !oldVersions.has(item.version));
  if (added.length === 0) {
    await emit({ publish: "false" });
    process.exit(0);
  }
  if (added.length !== 1) throw new Error(`release notes merge must add exactly one record; found ${added.length}`);
  const [record] = added;
  if (!stable.test(record.version) || record.channel !== "stable" || record.status !== "reviewed") {
    throw new Error("new release record must be a reviewed MAJOR.MINOR.PATCH official release");
  }
  version = record.version;
}

await emit({ publish: "true", version, tag: `v${version}`, sha, recovery: String(recovery) });
