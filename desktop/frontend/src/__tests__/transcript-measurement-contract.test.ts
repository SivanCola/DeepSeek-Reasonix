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
  styles.includes("\n  height: max(0px, calc(var(--transcript-row-estimate) - 32px))"),
  true,
  "pending Markdown uses only its bounded parser fallback height",
);
process.stdout.write("transcript measurement contract passed\n");
