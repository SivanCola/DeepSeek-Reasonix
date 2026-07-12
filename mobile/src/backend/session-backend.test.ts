import assert from "node:assert/strict";
import { LocalBackend } from "./session-backend.ts";
import { LOCAL_CAPABILITIES } from "../protocol/types.ts";

async function main() {
  const backend = new LocalBackend();
  assert.equal(backend.runtime, "local");

  await assert.rejects(
    () => backend.createSession({ runtime: "remote" }),
    /only creates local/,
  );

  const d = await backend.createSession({ runtime: "local", title: "t1" });
  assert.equal(d.runtime, "local");
  assert.deepEqual(d.capabilities, [...LOCAL_CAPABILITIES]);

  const events: unknown[] = [];
  const unsub = backend.subscribe(d.id, (e, seq) => {
    events.push({ e, seq });
  });
  await backend.submit(d.id, { text: "hello" }, "req-1");
  assert.ok(events.length >= 2);
  assert.equal((events[0] as { seq: number }).seq, 1);
  unsub();

  const snap = await backend.snapshot(d.id);
  assert.equal(snap.descriptor.id, d.id);
  assert.ok((snap.lastEventSeq ?? 0) >= 1);

  console.log("session-backend.test.ts: ok");
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
