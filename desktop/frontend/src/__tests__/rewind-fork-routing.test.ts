// Run: tsx src/__tests__/rewind-fork-routing.test.ts

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const appSource = readFileSync(resolve(testDir, "../App.tsx"), "utf8");
const controllerSource = readFileSync(resolve(testDir, "../lib/useController.ts"), "utf8");

assert.match(appSource, /const targetTabId = outcome\.tabId \|\| sourceTabId/);
assert.match(appSource, /undoTabId: sourceTabId/);
assert.match(appSource, /const outcome = await rewindForTabDetailed\(sourceTabId, turn, "conversation"\)/);
assert.match(appSource, /sendToTab\(targetTabId, next, submit, original\)/);
assert.match(controllerSource, /adoptReturnedTab\(outcome\.tab, sourceTabId, forkNavigationSeq, "tab\.rewind"\)/);

console.log("rewind fork routing contract passed");
