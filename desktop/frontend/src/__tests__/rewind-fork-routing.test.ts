// Run: tsx src/__tests__/rewind-fork-routing.test.ts

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { partialRewindNotice } from "../lib/rewindCommit";

const testDir = dirname(fileURLToPath(import.meta.url));
const appSource = readFileSync(resolve(testDir, "../App.tsx"), "utf8");
const controllerSource = readFileSync(resolve(testDir, "../lib/useController.ts"), "utf8");

assert.match(appSource, /const targetTabId = outcome\.tabId \|\| sourceTabId/);
assert.match(appSource, /undoTabId: sourceTabId/);
assert.match(appSource, /const outcome = await rewindForTabDetailed\(sourceTabId, turn, "conversation"\)/);
assert.match(appSource, /sendToTab\(targetTabId, next, submit, original\)/);
assert.match(controllerSource, /adoptReturnedTab\(outcome\.tab, sourceTabId, forkNavigationSeq, "tab\.rewind"\)/);
assert.match(controllerSource, /partialNotice = partialRewindNotice\(result\)/);
assert.match(controllerSource, /dispatchTo\(sourceTabId, \{ type: "local_notice", level: "warn", text: partialNotice \}\)/);
assert.match(controllerSource, /dispatchTo\(outcome\.tabId, \{ type: "local_notice", level: "warn", text: partialNotice \}\)/);
assert.ok(
  controllerSource.indexOf('await loadSessionDataForTab(sourceTabId, true, "rewind")')
    < controllerSource.indexOf('dispatchTo(sourceTabId, { type: "local_notice", level: "warn", text: partialNotice })'),
  "partial rewind warning must be appended after history reload",
);
assert.equal(partialRewindNotice({ ok: true }), "");
assert.equal(
  partialRewindNotice({ ok: true, partial: true, conflicts: ["a.txt changed", "b.txt missing"] }),
  "The conversation was forked, but code could not be fully restored. a.txt changed; b.txt missing",
);

console.log("rewind fork routing contract passed");
