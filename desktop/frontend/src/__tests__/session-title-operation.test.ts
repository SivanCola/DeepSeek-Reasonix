import assert from "node:assert/strict";
import { sessionTitleTarget, sessionTitleErrorKey } from "../lib/sessionTitleOperation";
import type { ProjectNode } from "../lib/types";

const first: ProjectNode = { key: "first", kind: "topic", label: "First", topicId: "shared", sessionPath: "legacy", session: { hostId: "local", sessionId: "first" } };
const second = { ...first, key: "second", session: { hostId: "local", sessionId: "second" } };
assert.equal(sessionTitleTarget(first), "session-id:first");
assert.notEqual(sessionTitleTarget(first), sessionTitleTarget(second));
assert.equal(sessionTitleTarget({ ...first, session: undefined }), "legacy");
assert.equal(sessionTitleTarget({ ...first, session: undefined, sessionPath: undefined }), "shared");
assert.equal(sessionTitleErrorKey(new Error("session_operation:title_conflict:internal details")), "projectTree.sessionError.titleConflict");
for (const error of [new Error("failed to read /private/fixture/session"), "lease owner controller test-owner", "provider secret test-token"]) {
  assert.equal(sessionTitleErrorKey(error), "projectTree.sessionError.failed");
}
console.log("session title target precedence, sibling isolation and sanitized errors passed");
