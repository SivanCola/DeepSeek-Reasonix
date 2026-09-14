import type {
  MessageHistoryPage,
  MessageLocation,
  Ref as SessionContentRef,
  SearchHistoryPage,
  SessionHistoryContentChunk,
  SessionOpenView,
} from "../generated/desktopContract.generated";

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

export function makeMockSessionReaderBindings(): SessionReaderBindings {
  const open = (hostId: string): SessionOpenView => ({
    session: { hostId, sessionId: "mock" }, storageGeneration: "mock",
    snapshotSequence: 0, acceptedSequence: 0, durableSequence: 0,
    recent: { version: 1, sessionId: "mock", storageGeneration: "mock", durableSequence: 0, totalTurns: 0, entries: [] },
    recovery: "ready", history: "preparing", search: "preparing", canExecute: true,
  });
  const page = (): MessageHistoryPage => ({
    messages: [], snapshotSequence: 0, coverageSequence: 0, status: "preparing",
    totalTurns: 0, generation: "", hasMore: false,
  });
  const search = (): SearchHistoryPage => ({ hits: [], snapshotSequence: 0, coverageSequence: 0, status: "preparing", hasMore: false });
  return {
    async SessionHistoryPageForTab() { return page(); },
    async SessionOpenForTab() { return open("local"); },
    async SessionHistoryContentForTab(_tabID, ref, offset) { return { data: "", nextOffset: Math.min(offset, ref.bytes), done: offset >= ref.bytes }; },
    async RemoteSessionOpenForTab() { return open("remote"); },
    async RemoteSessionHistoryPageForTab() { return page(); },
    async RemoteSessionHistoryContentForTab(_tabID, ref, offset) { return { data: "", nextOffset: Math.min(offset, ref.bytes), done: offset >= ref.bytes }; },
    async SearchSessionHistoryForTab() { return search(); },
    async RemoteSearchSessionHistoryForTab() { return search(); },
    async LocateSessionMessageForTab(_tabID, messageID) { return { status: "not_found", messageId: messageID, snapshotSequence: 0, coverageSequence: 0 }; },
    async RemoteLocateSessionMessageForTab(_tabID, messageID) { return { status: "not_found", messageId: messageID, snapshotSequence: 0, coverageSequence: 0 }; },
  };
}
