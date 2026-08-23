import { readFileSync } from "node:fs";
import { join } from "node:path";
import assert from "node:assert/strict";

const root = join(import.meta.dirname, "..", "..");
const arbiter = readFileSync(join(root, "src/lib/useTranscriptScrollArbiter.ts"), "utf8");
const styles = readFileSync(join(root, "src/styles.css"), "utf8");

assert.equal(
  arbiter.includes('style.setProperty("height"'),
  false,
  "manual transcript scrolling must not force a recycled row height",
);
assert.equal(
  styles.includes("data-transcript-geometry-pending"),
  false,
  "pending Markdown must not create a second fixed-height geometry contract",
);
process.stdout.write("transcript measurement contract passed\n");
