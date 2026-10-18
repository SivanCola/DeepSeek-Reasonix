import assert from "node:assert/strict";
import { Writable } from "node:stream";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { createLogger, createOutputEcho, RotatingFile } from "./log.js";

test("broken stderr is disabled once and file diagnostics remain available", async () => {
  const dir = mkdtempSync(join(tmpdir(), "reasonix-log-"));
  const file = new RotatingFile(join(dir, "shell.log"));
  let writes = 0;
  const broken = new Writable({ write(_chunk, _encoding, callback) {
    writes++;
    callback(Object.assign(new Error("broken pipe"), { code: "EPIPE" }));
  } });
  try {
    const echo = createOutputEcho(broken);
    const log = createLogger(file, echo);
    log.error("original failure");
    await new Promise(resolve => setImmediate(resolve));
    log.error("subsequent failure");
    echo("service stderr");
    assert.equal(writes, 1);
    assert.match(readFileSync(file.path, "utf8"), /original failure[\s\S]*subsequent failure/);
  } finally { file.close(); rmSync(dir, { recursive: true, force: true }); }
});

test("synchronous output failure never escapes into uncaughtException", () => {
  const sink = new Writable();
  let attempts = 0;
  sink.write = (() => { attempts++; throw new Error("write EIO"); }) as typeof sink.write;
  const echo = createOutputEcho(sink);
  assert.doesNotThrow(() => { echo("first"); echo("second"); });
  assert.equal(attempts, 1);
});

test("backpressure bounds pending output and resumes after drain", () => {
  let pending!: () => void;
  const chunks: string[] = [];
  const sink = new Writable({ highWaterMark: 1, write(chunk, _encoding, callback) {
    chunks.push(String(chunk)); pending = callback;
  } });
  const echo = createOutputEcho(sink);
  echo("first");
  for (let i = 0; i < 1000; i++) echo("must not buffer");
  assert.deepEqual(chunks, ["first"]);
  pending();
  sink.emit("drain");
  echo("resumed");
  assert.deepEqual(chunks, ["first", "resumed"]);
  pending();
});
