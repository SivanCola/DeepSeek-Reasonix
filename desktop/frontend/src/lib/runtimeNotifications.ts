import { runtimeStateStore, type RuntimeSession } from "./runtimeStateStore";
import { clearAttentionChimeKeys, playAttentionChime, playSuccessChime, shouldPlayAttentionChimeForEvent, type AttentionChimeEvent } from "./sound";
import type { Translator } from "./i18n";
import type { ToastContextValue } from "./toast";

export type NotificationOperation = { event: AttentionChimeEvent & { err?: string } } | { resetTabId?: string };
type NotificationPorts = { activeTabId: string | undefined; t: Translator; showToast: ToastContextValue["showToast"] };

/** One owner deduplicates view events and authoritative background snapshots. */
export function createRuntimeNotifications(readPorts: () => NotificationPorts | undefined) {
  const seen = new Set<string>();
  const handleAttention = (event: AttentionChimeEvent, source?: RuntimeSession) => {
    const ports = readPorts();
    if (!ports || !shouldPlayAttentionChimeForEvent(event, seen)) return;
    playAttentionChime();
    const { activeTabId, t, showToast } = ports;
    const snapshot = runtimeStateStore.getSnapshot();
    const turnId = event.ask?.turnId || event.approval?.turnId || event.turnId;
    const session = source ?? snapshot?.sessions.find(item => turnId ? item.state.turnId === turnId : item.open && item.tabId === event.tabId);
    const background = session ? !session.open || session.tabId !== activeTabId : Boolean(event.tabId && event.tabId !== activeTabId);
    if (!background) return;
    const topic = session?.topicId ? snapshot?.topics.find(item => item.scope === session.scope
      && (session.scope !== "project" || (item.workspaceRoot ?? "") === session.workspaceRoot)
      && item.node.topicId === session.topicId) : undefined;
    const child = session?.sessionPath ? topic?.node.children?.find(node => node.sessionPath === session.sessionPath) : undefined;
    const title = child?.label || topic?.node.label || t("runtime.otherConversation");
    showToast(t(event.kind === "ask_request" ? "runtime.backgroundQuestion" : "runtime.backgroundApproval", { title }), "info", { durationMs: 8000 });
  };
  const handleSnapshot = () => {
    if (runtimeStateStore.getFailed()) return;
    for (const session of runtimeStateStore.getSnapshot()?.sessions ?? []) {
      if (session.freshness !== "synced" || !session.state.pendingPrompt) continue;
      for (const prompt of session.state.pendingInteractions ?? []) {
        const identity = { id: prompt.requestId, turnId: prompt.turnId || session.state.turnId };
        // Plan/recovery decisions use the same approval card/event path.
        if (prompt.kind === "ask") handleAttention({ kind: "ask_request", tabId: session.tabId, ask: identity }, session);
        else if (["approval", "plan", "recovery"].includes(prompt.kind)) {
          handleAttention({ kind: "approval_request", tabId: session.tabId, approval: identity }, session);
        }
      }
    }
  };
  let stop: (() => void) | undefined;
  return {
    accept(operation: NotificationOperation) {
      if (!("event" in operation)) clearAttentionChimeKeys(seen, operation.resetTabId);
      else if (operation.event.kind === "turn_done") {
        if (readPorts() && !operation.event.err) playSuccessChime();
      } else handleAttention(operation.event);
    },
    start() { stop = runtimeStateStore.subscribe(handleSnapshot); handleSnapshot(); },
    dispose() { stop?.(); },
  };
}
