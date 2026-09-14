import type {
  MessageHistoryPage,
  MessageLocation,
  PersistentMessage,
  Ref as SessionContentRef,
  SearchHistoryPage,
  SessionHistoryContentChunk,
  SessionOpenView,
} from "../generated/desktopContract.generated";
import type { HistoryContentChunk, HistoryContentRef, HistoryMessage, HistorySlice, HistorySliceRequest } from "./types";

export interface SessionReaderBindings {
  SessionHistoryPageForTab(tabID: string, cursor: string, limit: number): Promise<MessageHistoryPage>;
  SessionOpenForTab(tabID: string): Promise<SessionOpenView>;
  SessionHistoryContentForTab(tabID: string, ref: SessionContentRef, offset: number): Promise<SessionHistoryContentChunk>;
  RemoteSessionOpenForTab(tabID: string): Promise<SessionOpenView>;
  RemoteSessionHistoryPageForTab(tabID: string, cursor: string, limit: number): Promise<MessageHistoryPage>;
  RemoteSessionHistoryContentForTab(tabID: string, ref: SessionContentRef, offset: number): Promise<SessionHistoryContentChunk>;
  SearchSessionHistoryForTab(tabID: string, textQuery: string, cursor: string, limit: number): Promise<SearchHistoryPage>;
  RemoteSearchSessionHistoryForTab(tabID: string, textQuery: string, cursor: string, limit: number): Promise<SearchHistoryPage>;
  LocateSessionMessageForTab(tabID: string, messageID: string, snapshot: number): Promise<MessageLocation>;
  RemoteLocateSessionMessageForTab(tabID: string, messageID: string, snapshot: number): Promise<MessageLocation>;
}

interface MockSessionReaderHost {
  HistoryForTab?(tabID: string): Promise<HistoryMessage[]>;
  HistorySliceForTab(tabID: string, req: HistorySliceRequest): Promise<HistorySlice>;
  HistoryContentForTab(tabID: string, ref: HistoryContentRef, chunkIndex: number): Promise<HistoryContentChunk>;
  SessionHistoryPageForTab(tabID: string, cursor: string, limit: number): Promise<MessageHistoryPage>;
  SessionOpenForTab(tabID: string): Promise<SessionOpenView>;
  SessionHistoryContentForTab(tabID: string, ref: SessionContentRef, offset: number): Promise<SessionHistoryContentChunk>;
}

function messageIndex(entryId: string): number {
  return Number(/:m(\d+):o\d+$/.exec(entryId)?.[1] ?? -1);
}

function canonicalBody(message: HistoryMessage | undefined, messageId: string): Uint8Array {
  if (!message) return new Uint8Array();
  const body = {
    ...message,
    id: message.messageId ?? messageId,
    reasoning_content: message.reasoning,
    tool_calls: message.toolCalls?.map(call => ({
      ...call,
      resolved_name: call.resolvedName,
      capability_id: call.capabilityId,
      resolved_read_only: call.resolvedReadOnly,
    })),
    tool_call_id: message.toolCallId,
    name: message.toolName,
    tool_execution: message.execution,
    presented_files: message.presentedFiles ? { files: message.presentedFiles } : undefined,
    server_search: message.serverSearch,
  };
  return new TextEncoder().encode(JSON.stringify(body));
}

function persistentMessages(slice: HistorySlice, history: HistoryMessage[]): PersistentMessage[] {
  return slice.entries.map(entry => {
    const index = messageIndex(entry.entryId);
    const messageId = entry.message.messageId ?? entry.entryId;
    const body = canonicalBody(history[index], messageId);
    return {
      messageId, position: entry.order,
      version: 1, role: entry.message.role, preview: entry.message.content ?? "",
      eventSequence: 0, visibleTurn: entry.turn, inline: JSON.parse(new TextDecoder().decode(canonicalBody(entry.message, messageId))),
      contentRef: (entry.refs ?? []).length > 0 ? { digest: `mock-canonical:${index}`, bytes: body.length, mediaType: "application/json" } : undefined,
    };
  });
}

function encodedChunk(bytes: Uint8Array): string {
  let binary = "";
  for (let start = 0; start < bytes.length; start += 0x8000) binary += String.fromCharCode(...bytes.subarray(start, start + 0x8000));
  return btoa(binary);
}

export function makeMockSessionReaderBindings(): SessionReaderBindings {
  const search = (): SearchHistoryPage => ({ hits: [], snapshotSequence: 0, coverageSequence: 0, status: "preparing", hasMore: false });
  return {
    async SessionHistoryPageForTab(this: MockSessionReaderHost, tabID, cursor, limit) {
      const slice = await this.HistorySliceForTab(tabID, { cursor, entries: limit, turns: limit });
      const history = slice.entries.some(entry => entry.refs?.length) ? await this.HistoryForTab?.(tabID) ?? [] : [];
      return { messages: persistentMessages(slice, history), snapshotSequence: slice.revision, coverageSequence: slice.revision, status: "ready", totalTurns: slice.totalTurns, generation: slice.digest ?? "", nextCursor: slice.nextCursor, hasMore: slice.hasOlder };
    },
    async SessionOpenForTab(this: MockSessionReaderHost, tabID) {
      const slice = await this.HistorySliceForTab(tabID, { cursor: "", entries: 100, turns: 100 });
      const history = slice.entries.some(entry => entry.refs?.length) ? await this.HistoryForTab?.(tabID) ?? [] : [];
      const entries = persistentMessages(slice, history);
      return { session: { hostId: "local", sessionId: tabID }, storageGeneration: slice.digest, snapshotSequence: slice.revision, acceptedSequence: slice.revision, durableSequence: slice.revision, recent: { version: 1, sessionId: tabID, storageGeneration: slice.digest ?? "", durableSequence: slice.revision, totalTurns: slice.totalTurns, entries }, recovery: "ready", history: "ready", search: "preparing", canExecute: true };
    },
    async SessionHistoryContentForTab(this: MockSessionReaderHost, tabID, ref, offset) {
      const index = Number(ref.digest.replace("mock-canonical:", ""));
      const history = await this.HistoryForTab?.(tabID) ?? [];
      const message = history[index];
      const entryId = `smock-${tabID}:r0:m${index}:o0`;
      await this.HistoryContentForTab(tabID, { entryId, field: "content", size: message?.content?.length ?? 0, chunks: 1, revision: 0, digest: "mock" }, 0);
      const body = canonicalBody(message, entryId);
      const nextOffset = Math.min(body.length, offset + (1 << 20));
      return { data: encodedChunk(body.subarray(offset, nextOffset)), nextOffset, done: nextOffset >= body.length };
    },
    async RemoteSessionOpenForTab(this: MockSessionReaderHost, tabID) { return this.SessionOpenForTab(tabID); },
    async RemoteSessionHistoryPageForTab(this: MockSessionReaderHost, tabID, cursor, limit) { return this.SessionHistoryPageForTab(tabID, cursor, limit); },
    async RemoteSessionHistoryContentForTab(this: MockSessionReaderHost, tabID, ref, offset) { return this.SessionHistoryContentForTab(tabID, ref, offset); },
    async SearchSessionHistoryForTab() { return search(); },
    async RemoteSearchSessionHistoryForTab() { return search(); },
    async LocateSessionMessageForTab(_tabID, messageID) { return { status: "not_found", messageId: messageID, snapshotSequence: 0, coverageSequence: 0 }; },
    async RemoteLocateSessionMessageForTab(_tabID, messageID) { return { status: "not_found", messageId: messageID, snapshotSequence: 0, coverageSequence: 0 }; },
  };
}
