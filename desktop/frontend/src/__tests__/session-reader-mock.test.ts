import assert from "node:assert/strict";
import { canonicalMessage } from "../lib/canonicalTranscriptBackend";
import { mockHistoryContentField, mockHistorySlice } from "../lib/bridgeHistoryFixtures";
import { makeMockSessionReaderBindings } from "../lib/sessionReaderBridge";
import type { HistoryContentRef, HistoryMessage, HistorySliceRequest } from "../lib/types";

const marker = "ASYNC LAYOUT EXPANSION COMPLETE";
const messages: HistoryMessage[] = [
  { role: "user", content: "question" },
  { role: "assistant", content: `${"preview ".repeat(700)}\n${marker}`, reasoning: "complete reasoning" },
];
let contentReads = 0;
const host = Object.assign({
  async HistoryForTab() { return messages; },
  async HistorySliceForTab(tabID: string, req: HistorySliceRequest) { return mockHistorySlice(tabID, messages, req, true); },
  async HistoryContentForTab(_tabID: string, ref: HistoryContentRef) {
    contentReads += 1;
    return { entryId: ref.entryId, field: ref.field, chunk: 0, chunks: 1, data: mockHistoryContentField(messages[1], ref), done: true, stale: false };
  },
}, makeMockSessionReaderBindings());

const view = await host.SessionOpenForTab("tab-1");
assert.equal(view.recent.entries.length, 2);
const persistent = view.recent.entries[1];
assert.ok(persistent.contentRef, "lazy fixture keeps a canonical content reference");
assert.equal((persistent.inline as HistoryMessage | undefined)?.content.includes(marker), false, "open response contains only the legacy preview");

const chunk = await host.SessionHistoryContentForTab("tab-1", persistent.contentRef!, 0);
assert.equal(contentReads, 1, "canonical hydration preserves the legacy asynchronous read path");
const decoded = new TextDecoder().decode(Uint8Array.from(atob(chunk.data), character => character.charCodeAt(0)));
const hydrated = canonicalMessage(persistent, JSON.parse(decoded));
assert.equal(hydrated.messageId, persistent.messageId, "hydration keeps the stable message identity");
assert.ok(hydrated.content.includes(marker));
assert.equal(hydrated.reasoning, "complete reasoning");

console.log("PASS protocol 7 mock sessions preserve benchmark history and lazy hydration");
