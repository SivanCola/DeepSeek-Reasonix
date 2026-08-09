#!/usr/bin/env node

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "../../..");
const workflow = readFileSync(resolve(repoRoot, ".github/workflows/ci.yml"), "utf8");
const packageJSON = JSON.parse(readFileSync(resolve(repoRoot, "desktop/frontend/package.json"), "utf8"));

function jobBody(name, nextName) {
  const match = workflow.match(new RegExp(`\\n  ${name}:\\n([\\s\\S]*?)\\n  ${nextName}:`));
  if (!match) throw new Error(`motion-ci-contract: could not locate ${name} job`);
  return match[1];
}

for (const [job, body, command] of [
  ["desktop", jobBody("desktop", "desktop-macos"), "pnpm --dir frontend test:motion"],
  ["desktop-windows", jobBody("desktop-windows", "lint"), "pnpm --dir frontend test:motion"],
  ["required lint", jobBody("lint", "site"), "pnpm --dir desktop/frontend test:motion"],
]) {
  if (!body.includes(command)) {
    throw new Error(`motion-ci-contract: ${job} must run test:motion`);
  }
}

const windowsJob = jobBody("desktop-windows", "lint");
for (const required of [
  "wails build -clean -s -skipbindings -nopackage -platform windows/amd64 -webview2 embed",
  "Smoke-test Wails approval in WebView2",
  "../scripts/test-webview2-approval-smoke.ps1",
]) {
  if (!windowsJob.includes(required)) {
    throw new Error(`motion-ci-contract: desktop-windows must include ${required}`);
  }
}

const motionScript = packageJSON.scripts?.["test:motion"] ?? "";
for (const required of [
  "check-waapi-contract.mjs --self-test",
  "native-motion.test.tsx",
  "approval-animation.test.tsx",
  "transcript-virtualization.test.tsx",
]) {
  if (!motionScript.includes(required)) {
    throw new Error(`motion-ci-contract: test:motion must include ${required}`);
  }
}

console.log("motion-ci-contract: required lint plus Linux and Windows jobs run native motion gates, and Windows runs the real WebView2 smoke");
