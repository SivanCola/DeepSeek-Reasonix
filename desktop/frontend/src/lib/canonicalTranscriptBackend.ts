import type { MessageHistoryPage, PersistentMessage } from "../generated/desktopContract.generated";
import { asArray } from "./array";
import { app } from "./bridge";
import type { HistoryContentChunk, HistoryContentRef, HistoryEntry, HistoryMessage, HistorySlice, HistorySliceRequest, MemoryCitation } from "./types";

function asWireObject(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

export function canonicalMessage(message: PersistentMessage, body: unknown): HistoryMessage {
  const raw = asWireObject(body);
  const decisionReceipt = asWireObject(raw.decision_receipt);
  if (Object.keys(decisionReceipt).length > 0) {
    return { role: "notice", messageId: String(raw.id ?? message.messageId), content: "", code: "decision_receipt", level: "info", decisionReceipt: decisionReceipt as unknown as HistoryMessage["decisionReceipt"] };
  }
  const readPause = asWireObject(raw.read_pause);
  if (Boolean(raw.local_only) && Object.keys(readPause).length > 0) {
    return { role: "notice", messageId: String(raw.id ?? message.messageId), content: "", code: "incomplete_read", level: "info", readPause: readPause as unknown as HistoryMessage["readPause"] };
  }
  const readiness = asWireObject(raw.final_readiness_recovery);
  if (Boolean(raw.local_only) && readiness.pending === true) {
    return { role: "notice", messageId: String(raw.id ?? message.messageId), content: "Final checks are still required before this task is complete.", code: "historical_checks", level: "info", readiness: { missing: Array.isArray(readiness.missing) ? readiness.missing.map(String) : undefined } };
  }
  const protocolRecovery = asWireObject(raw.protocol_recovery);
  if (Boolean(raw.local_only) && protocolRecovery.state === "pending" && typeof protocolRecovery.id === "string") {
    return { role: "notice", messageId: String(raw.id ?? message.messageId), content: "", code: "protocol_recovery", level: "info", pending: true, protocolRecovery: { id: protocolRecovery.id } };
  }
  const toolCalls = (Array.isArray(raw.tool_calls) ? raw.tool_calls as Record<string, unknown>[] : []).map(call => ({
    id: String(call.id ?? ""), name: String(call.name ?? ""), arguments: String(call.arguments ?? ""),
    resolvedName: typeof call.resolved_name === "string" ? call.resolved_name : undefined,
    capabilityId: typeof call.capability_id === "string" ? call.capability_id : undefined,
    resolvedReadOnly: typeof call.resolved_read_only === "boolean" ? call.resolved_read_only : undefined,
    diff: typeof call.diff === "string" ? call.diff : undefined,
    added: typeof call.added === "number" ? call.added : undefined,
    removed: typeof call.removed === "number" ? call.removed : undefined,
  }));
  const presented = asWireObject(raw.presented_files);
  return {
    role: Boolean(raw.local_only) ? "assistant" : String(raw.role ?? message.role),
    messageId: String(raw.id ?? message.messageId),
    content: String(raw.content ?? raw.raw_content ?? message.preview ?? ""),
    reasoning: typeof raw.reasoning_content === "string" ? raw.reasoning_content : undefined,
    createdAt: typeof raw.createdAt === "number" ? raw.createdAt : undefined,
    workDurationMs: typeof raw.workDurationMs === "number" ? raw.workDurationMs : undefined,
    toolCalls: toolCalls.length > 0 ? toolCalls : undefined,
    toolCallId: typeof raw.tool_call_id === "string" ? raw.tool_call_id : undefined,
    toolName: typeof raw.name === "string" ? raw.name : undefined,
    memoryCitations: Array.isArray(raw.memoryCitations) ? raw.memoryCitations as MemoryCitation[] : undefined,
    serverSearch: Array.isArray(raw.server_search) ? raw.server_search as HistoryMessage["serverSearch"] : undefined,
    execution: Object.keys(asWireObject(raw.tool_execution)).length > 0 ? raw.tool_execution as HistoryMessage["execution"] : undefined,
    presentedFiles: Array.isArray(presented.files) ? presented.files as HistoryMessage["presentedFiles"] : undefined,
    readCompletion: Object.keys(asWireObject(raw.read_completion)).length > 0 ? raw.read_completion as HistoryMessage["readCompletion"] : undefined,
  };
}

export function resolvedHistoryField(message: HistoryMessage, field: string): string | undefined {
  switch (field) {
    case "content": return message.content;
    case "reasoning": return message.reasoning;
    case "submitText": return message.submitText;
    case "detail": return message.detail;
    case "code": return message.code;
    case "summary": return message.summary;
    case "archive": return message.archive;
    case "toolResultError": return message.toolResultError;
    default: return message.content;
  }
}

async function historyPage(tabId: string, cursor: string, limit: number): Promise<MessageHistoryPage> {
  try {
    return await app.SessionHistoryPageForTab(tabId, cursor, limit);
  } catch (localError) {
    try { return await app.RemoteSessionHistoryPageForTab(tabId, cursor, limit); } catch { throw localError; }
  }
}

async function openSession(tabId: string) {
  try {
    return await app.SessionOpenForTab(tabId);
  } catch (localError) {
    try { return await app.RemoteSessionOpenForTab(tabId); } catch { throw localError; }
  }
}

const locatorResetCursor = "reasonix:locator:newest";

function entriesFor(messages: PersistentMessage[], snapshotSequence: number): HistoryEntry[] {
  return messages.map(persistent => {
    const entryId = `m:${persistent.messageId}`;
    return {
      entryId, turn: persistent.visibleTurn ?? 0, order: persistent.position,
      message: canonicalMessage(persistent, persistent.inline),
      refs: persistent.contentRef ? [{
        entryId, field: "canonicalMessage", size: persistent.contentRef.bytes,
        chunks: Math.max(1, Math.ceil(persistent.contentRef.bytes / (1 << 20))),
        revision: snapshotSequence, revKnown: true, digest: persistent.contentRef.digest,
        canonicalRef: persistent.contentRef,
      }] : [],
    };
  });
}

export async function canonicalHistorySlice(tabId: string, req: HistorySliceRequest): Promise<HistorySlice> {
  const cursor = req.cursor ?? "";
  const limit = Math.min(500, Math.max(1, req.entries ?? 100));
  if (cursor === "") {
    const view = await openSession(tabId);
    const recent = asArray<PersistentMessage>(view.recent.entries);
    if (view.recent) {
      const entries = entriesFor(recent, view.snapshotSequence);
      const turns = entries.map(entry => entry.turn).filter(turn => turn > 0);
      const startTurn = turns.length > 0 ? Math.min(...turns) : 0;
      const hasOlder = startTurn > 1 || (recent[0]?.position ?? 0) > 0;
      return {
        entries, nextCursor: hasOlder ? locatorResetCursor : "", hasOlder,
        totalTurns: view.recent.totalTurns > 0 ? view.recent.totalTurns : (turns.length > 0 ? Math.max(...turns) : 0),
        startTurn, endTurn: turns.length > 0 ? Math.max(...turns) : 0, stale: false,
        revision: view.snapshotSequence, revisionKnown: view.snapshotSequence > 0,
        digest: view.storageGeneration ?? view.recent.storageGeneration, source: "recent",
      };
    }
  }
  const reset = cursor === locatorResetCursor;
  const page = await historyPage(tabId, reset ? "" : cursor, limit);
  if (page.status === "stale_cursor") return { entries: [], nextCursor: "", hasOlder: false, totalTurns: 0, startTurn: 0, endTurn: 0, stale: true, revision: 0 };
  if (page.status && page.status !== "ready") throw new Error(`Session history is ${page.status}`);
  const entries = entriesFor(asArray<PersistentMessage>(page.messages), page.snapshotSequence);
  const turns = entries.map(entry => entry.turn).filter(turn => turn > 0);
  return {
    entries, nextCursor: page.nextCursor ?? "", hasOlder: page.hasMore,
    totalTurns: page.totalTurns ?? (turns.length > 0 ? Math.max(...turns) : 0),
    startTurn: turns.length > 0 ? Math.min(...turns) : 0,
    endTurn: turns.length > 0 ? Math.max(...turns) : 0, stale: false,
    revision: page.snapshotSequence, revisionKnown: page.snapshotSequence > 0, digest: page.generation,
    source: reset ? "locator-reset" : "locator",
  };
}

function decodeBase64Bytes(data: string): Uint8Array {
  const binary = atob(data);
  return Uint8Array.from(binary, character => character.charCodeAt(0));
}

export async function canonicalHistoryContent(tabID: string, ref: HistoryContentRef, chunkIndex: number): Promise<HistoryContentChunk> {
  if (!ref.canonicalRef) return app.HistoryContentForTab(tabID, ref, chunkIndex);
  const offset = chunkIndex * (1 << 20);
  let chunk;
  try {
    chunk = await app.SessionHistoryContentForTab(tabID, ref.canonicalRef, offset);
  } catch (localError) {
    try { chunk = await app.RemoteSessionHistoryContentForTab(tabID, ref.canonicalRef, offset); } catch { throw localError; }
  }
  const bytes = decodeBase64Bytes(chunk.data ?? "");
  let data = "";
  for (let start = 0; start < bytes.length; start += 0x8000) data += String.fromCharCode(...bytes.subarray(start, start + 0x8000));
  return { entryId: ref.entryId, field: ref.field, chunk: chunkIndex, chunks: ref.chunks, data, done: chunk.done, stale: false };
}
