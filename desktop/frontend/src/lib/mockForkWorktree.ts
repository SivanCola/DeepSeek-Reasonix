import type { ForkBindings, ForkWorktreeResultView } from "./forkWorktree";
import type { ForkCreationView, ForkTargetSetView, ForkTargetView } from "../generated/desktopContract.generated";
import type { HistoryMessage, TabMeta } from "./types";

export function mockForkWorktree(tab: TabMeta): ForkWorktreeResultView {
  return { tab: { ...tab, workspaceRoot: `${tab.workspaceRoot}-worktree` }, isolated: true, branch: "reasonix/delivery-mock" };
}

export function increaseMockForkTitle(title: string): string {
  const base = title.trim();
  const ascii = /^(.*) \(([0-9]+)\)$/.exec(base);
  if (ascii) return `${ascii[1]} (${BigInt(ascii[2]) + 1n})`;
  const fullwidth = /^(.*)（([0-9]+)）$/.exec(base);
  if (fullwidth) return `${fullwidth[1]}（${BigInt(fullwidth[2]) + 1n}）`;
  return `${base} (1)`;
}

/**
 * Fork targets for one mock source, derived the way the host derives them: one
 * target per user turn, addressed by the message identity of that turn's final
 * answer. The trailing turn stays open while the tab runs, so its entry explains
 * itself instead of offering a cut the source cannot prove. A source whose
 * messages carry no identity answers with an empty, unverifiable set.
 */
export function mockForkTargets(tabId: string, messages: readonly HistoryMessage[], running: boolean): ForkTargetSetView {
  const targets: ForkTargetView[] = [];
  let turn = 0;
  let answer: string | undefined;
  const close = (open: boolean) => {
    if (!turn) return;
    const turnId = `${tabId}:turn-${turn}`;
    targets.push(!open && answer
      ? { turnId, turnNumber: turn, status: "committed", messageId: answer, available: true }
      : { turnId, turnNumber: turn, status: open ? "in_progress" : "committed", available: false, reason: "turn_open" });
    answer = undefined;
  };
  for (const message of messages) {
    if (message.role === "user") { close(false); turn += 1; continue; }
    if (message.role === "assistant" && message.messageId && message.content?.trim()) answer = message.messageId;
  }
  close(running);
  return { targets, verifiable: messages.some((message) => Boolean(message.messageId)) };
}

// Browser-dev fork fixtures: an attach failure exercises the recovery notice,
// and a recordless source exercises the unverifiable boundary.
function mockForkFixture(name: string): boolean {
  return typeof window !== "undefined" && new URLSearchParams(window.location.search).get(name) === "1";
}

/**
 * The history a dev fork reads, with the identity the real pipeline assigns: a
 * rendered answer and the fork target that names it resolve through one key. The
 * recordless fixture drops those identities, which is the unverifiable boundary.
 */
export function withMockHistoryIds(prefix: string, messages: HistoryMessage[]): HistoryMessage[] {
  if (mockForkFixture("fork-recordless")) return messages;
  return messages.map((message, index) => message.role === "assistant" && !message.messageId
    ? { ...message, messageId: `${prefix}:m${index}` } : message);
}

interface MockForkBindings extends ForkBindings {
  Fork(turn: number): Promise<TabMeta>;
  ForkTargetsForTab(tabID: string): Promise<ForkTargetSetView>;
  CreateForkForTab(tabID: string, turnID: string, operationID: string): Promise<ForkCreationView>;
}

export function makeMockForkBindings(
  getTabs: () => TabMeta[],
  setTabs: (tabs: TabMeta[]) => void,
  defaultTitle: string,
  history: (tabID: string) => Promise<HistoryMessage[]>,
  attachFailure = mockForkFixture("fork-attach-failure"),
): MockForkBindings {
  const fork = async (_turn: number): Promise<TabMeta> => {
    const tabs = getTabs();
    const active = tabs.find((tab) => tab.active) ?? tabs[0];
    const stamp = Date.now();
    const tab: TabMeta = {
      ...active,
      id: `tab_fork_${stamp}`,
      topicId: `topic_fork_${stamp}`,
      topicTitle: increaseMockForkTitle(active.topicTitle || defaultTitle),
      active: true,
      running: false,
    };
    setTabs([...tabs.map((item) => ({ ...item, active: false })), tab]);
    return { ...tab };
  };
  const forkForTab = async (tabID: string, turn: number): Promise<TabMeta> => {
    setTabs(getTabs().map((tab) => ({ ...tab, active: tab.id === tabID })));
    return fork(turn);
  };
  return {
    Fork: fork,
    ForkForTab: forkForTab,
    async ForkWorktreeForTab(tabID, turn) {
      return mockForkWorktree(await forkForTab(tabID, turn));
    },
    // The targets come from the same history the transcript renders, so the
    // entry a developer clicks names the answer message on screen.
    async ForkTargetsForTab(tabID) {
      const tabs = getTabs();
      const tab = tabs.find((candidate) => candidate.id === tabID) ?? tabs.find((candidate) => candidate.active) ?? tabs[0];
      return mockForkTargets(tab?.id ?? tabID, await history(tab?.id ?? tabID), Boolean(tab?.running));
    },
    async CreateForkForTab(tabID, turnID, operationID) {
      if (!tabID || !turnID) return { opened: false };
      // The child id carries the operation id, so a repeated request names the
      // same child and a second user action creates its own.
      const sessionId = `mock-fork-${turnID}-${operationID}`;
      if (attachFailure) return { sessionId, opened: false, error: "conversation fork was created but could not be opened; open the recovery branch from session history" };
      const tab = await forkForTab(tabID, 0);
      return { sessionId, tabId: tab.id, opened: true };
    },
  };
}
