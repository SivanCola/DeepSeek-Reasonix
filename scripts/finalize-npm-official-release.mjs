#!/usr/bin/env node

import { execFileSync } from "node:child_process";

const version = process.argv[2];
if (!/^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(version || "")) {
  throw new Error("usage: finalize-npm-official-release.mjs MAJOR.MINOR.PATCH");
}
const packages = [
  "reasonix",
  "@reasonix/cli-darwin-arm64", "@reasonix/cli-darwin-x64",
  "@reasonix/cli-linux-arm64", "@reasonix/cli-linux-x64",
  "@reasonix/cli-win32-arm64", "@reasonix/cli-win32-x64",
];
for (const name of packages) {
  const actual = execFileSync("npm", ["view", `${name}@${version}`, "version"], { encoding: "utf8" }).trim();
  if (actual !== version) throw new Error(`${name}@${version} is unavailable`);
}
for (const name of packages) {
  for (const tag of ["latest", "canary", "next"]) {
    execFileSync("npm", ["dist-tag", "add", `${name}@${version}`, tag], { stdio: "inherit" });
  }
  const staging = execFileSync("npm", ["view", name, "dist-tags.official-staging", "--json"], { encoding: "utf8" }).trim();
  if (JSON.parse(staging || "null") === version) {
    execFileSync("npm", ["dist-tag", "rm", name, "official-staging"], { stdio: "inherit" });
  }
}
