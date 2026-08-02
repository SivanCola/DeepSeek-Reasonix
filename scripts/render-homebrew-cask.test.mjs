import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

test("renders one official cask from exact CLI checksums", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "reasonix-cask-"));
  const sums = path.join(dir, "SHA256SUMS");
  const output = path.join(dir, "reasonix.rb");
  const names = [
    "reasonix-darwin-amd64.tar.gz",
    "reasonix-darwin-arm64.tar.gz",
    "reasonix-linux-amd64.tar.gz",
    "reasonix-linux-arm64.tar.gz",
  ];
  fs.writeFileSync(sums, names.map((name, index) => `${String(index + 1).repeat(64)}  ${name}`).join("\n") + "\n");
  execFileSync(process.execPath, ["scripts/render-homebrew-cask.mjs", "1.2.3", sums, output]);
  const cask = fs.readFileSync(output, "utf8");
  assert.match(cask, /version "1\.2\.3"/);
  for (const name of names) assert.match(cask, new RegExp(name.replaceAll(".", "\\.")));
  assert.doesNotMatch(cask, /preview|canary|next/);
});

test("rejects incomplete checksum sets", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "reasonix-cask-"));
  const sums = path.join(dir, "SHA256SUMS");
  fs.writeFileSync(sums, `${"a".repeat(64)}  reasonix-darwin-amd64.tar.gz\n`);
  assert.throws(() => execFileSync(process.execPath, ["scripts/render-homebrew-cask.mjs", "1.2.3", sums, path.join(dir, "reasonix.rb")], { stdio: "pipe" }));
});
