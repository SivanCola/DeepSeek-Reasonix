// Forking a persisted turn: the child is created from one committed turn record
// and adopted without rewinding or switching the source tab, which is what
// separates this path from the switching fork in forkWorktree.ts. The tab keeps
// the identity of every child it published, so a repeat request recovers that
// child instead of creating a second one for the same turn.

import { asArray } from "./array";
import { app } from "./bridge";
import { errorMessage } from "./controllerNotices";
import { forkCreateFailureText, type ForkTargetSetView } from "./forkTargets";
import { t } from "./i18n";
import type { TabMeta } from "./types";
import type { ForkCreationView } from "../generated/desktopContract.generated";

/** The bridge commands a turn fork needs, so a caller can supply its own. */
export interface ForkTurnBindings {
  ListTabs(): Promise<TabMeta[]>;
  CreateForkForTab(tabID: string, turnID: string, operationID: string): Promise<ForkCreationView>;
}

/** The fork slice of a tab's controller state. */
export interface ForkTurnState {
  /** Persisted fork boundaries of the shown session; undefined until the first read resolves. */
  forkTargets?: ForkTargetSetView;
  /** A create-fork request for this tab is in flight. */
  forkCreating: boolean;
  /**
   * Child sessions this tab already published, keyed by the turn they were cut
   * from. A repeat fork of the same turn reuses the child instead of creating a
   * second one; a different turn creates its own.
   */
  forkChildren: Record<string, string>;
}

/** A tab that never forked holds no targets and no child. */
export const initialForkTurnState: ForkTurnState = { forkCreating: false, forkChildren: {} };

/** The fork actions a tab's reducer accepts, alongside every other action it handles. */
export type ForkTurnAction =
  | { type: "fork_targets"; targets: ForkTargetSetView }
  | { type: "fork_creating"; creating: boolean }
  | { type: "fork_child"; turnId: string; sessionId: string }

/** The fork slice's response to one fork action; an unchanged answer returns the same state. */
export function reduceForkTurn(state: ForkTurnState, action: ForkTurnAction): ForkTurnState {
  switch (action.type) {
    case "fork_targets": return { ...state, forkTargets: action.targets };
    case "fork_creating": return state.forkCreating === action.creating ? state : { ...state, forkCreating: action.creating };
    case "fork_child": return state.forkChildren[action.turnId] === action.sessionId
      ? state : { ...state, forkChildren: { ...state.forkChildren, [action.turnId]: action.sessionId } };
  }
}

/**
 * The per-tab read of fork boundaries. The host derives them from persisted turn
 * records, so the answer stays valid while a turn runs; a read that resolves
 * after a newer one for the same tab is dropped, because the newer read is the
 * one that reflects the current source.
 */
export function createForkTargetsRefresh(dispatch: (tabId: string, action: ForkTurnAction) => void): (tabId: string) => Promise<void> {
  const latestSeq = new Map<string, number>();
  return async (tabId) => {
    // A shell that predates the command reads no targets instead of failing the
    // transcript around it.
    if (typeof app.ForkTargetsForTab !== "function") return;
    const seq = (latestSeq.get(tabId) ?? 0) + 1;
    latestSeq.set(tabId, seq);
    const targets = await app.ForkTargetsForTab(tabId).catch(() => undefined);
    if (latestSeq.get(tabId) !== seq || targets === undefined) return;
    dispatch(tabId, { type: "fork_targets", targets: { targets: asArray(targets.targets), verifiable: Boolean(targets.verifiable) } });
  };
}

/** The notice a refused fork shows the user, dispatched like a fork action. */
export type ForkTurnNotice = { type: "local_notice"; level: "info" | "warn"; text: string };

/** What one fork request needs from the tab that owns it. */
export interface ForkTurnStep {
  /** The child this tab already published for the turn, when a repeat request recovers it. */
  rememberedChild?: string;
  /** Applies a fork action, or the notice a refusal shows, to the source tab's state. */
  dispatch(action: ForkTurnAction | ForkTurnNotice): void;
  /** Brings the tab the host opened for the child to the foreground. */
  adopt(tab: TabMeta): Promise<unknown>;
  /** Re-reads the active tab from the backend, for a child that opened without being adopted. */
  sync(): Promise<unknown>;
  /** Resolves once the given tab's runtime accepts a fork. */
  waitForTabReady(tabId: string): Promise<unknown>;
}

// The host answers with the opened tab's id rather than its meta, so the tab
// list supplies the identity, with one moment for the registry to publish a tab
// the host reports as already open.
async function listedTab(bindings: ForkTurnBindings, tabId: string): Promise<TabMeta | undefined> {
  for (let attempt = 0; attempt < 5; attempt += 1) {
    const tab = asArray(await bindings.ListTabs().catch(() => [] as TabMeta[])).find((candidate) => candidate.id === tabId);
    if (tab) return tab;
    await new Promise((resolve) => window.setTimeout(resolve, 50));
  }
  return undefined;
}

/**
 * Creates an independent child session from one persisted turn of a source tab
 * and adopts the tab the host opened for it. The source keeps its transcript,
 * its running turn, and its controller, so this path never rewinds or switches
 * the parent. Every failure reaches the user through the same notice channel
 * the switching fork used.
 */
export async function settleForkTurnForTab(
  bindings: ForkTurnBindings,
  sourceTabId: string,
  turnId: string,
  step: ForkTurnStep,
): Promise<boolean> {
  if (!sourceTabId || !turnId) return false;
  // A child this turn already published is reused, never duplicated: a local
  // child whose tab never opened can only be recovered by name — the frontend
  // holds no local path that would open that session again.
  if (step.rememberedChild) {
    step.dispatch({ type: "local_notice", level: "warn", text: t("chat.branchRecoverChild", { session: step.rememberedChild }) });
    return false;
  }
  step.dispatch({ type: "fork_creating", creating: true });
  try {
    await step.waitForTabReady(sourceTabId);
    // A fresh operation id per user action: the host reuses the child a
    // repeated request names, so a retry can never publish a second fork of
    // one turn, while a second click stays a second creation.
    const created = await bindings.CreateForkForTab(sourceTabId, turnId, crypto.randomUUID());
    if (created?.opened && created.tabId) {
      const tab = await listedTab(bindings, created.tabId);
      if (tab) {
        await step.adopt(tab);
        return true;
      }
    }
    // The child becomes durable before its tab opens, so it is remembered and
    // recovered by name: creating it again would publish a second fork of the
    // same turn.
    if (created?.sessionId) {
      step.dispatch({ type: "fork_child", turnId, sessionId: created.sessionId });
      step.dispatch({ type: "local_notice", level: "warn", text: t("chat.branchRecoverChild", { session: created.sessionId }) });
      if (created.opened) await step.sync();
      return false;
    }
    const detail = errorMessage(created?.error ?? "");
    step.dispatch({ type: "local_notice", level: "warn", text: detail ? t("chat.branchFailedDetail", { detail }) : t("chat.branchFailed") });
    return false;
  } catch (error) {
    step.dispatch({ type: "local_notice", level: "warn", text: forkCreateFailureText(error) });
    return false;
  } finally {
    step.dispatch({ type: "fork_creating", creating: false });
  }
}
