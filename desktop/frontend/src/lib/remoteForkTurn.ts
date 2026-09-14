// The remote turn fork: the serve owns the child, so the frontend reads which
// turns it may fork from and asks for one child per turn. A child the serve
// created but could not surface is remembered by name, because only its id can
// reopen it, and /fork is never used: that route switches the parent session.

import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { forkCreateFailureText, type ForkTargetSetView } from "./forkTargets";
import { t } from "./i18n";
import { reducer, type State } from "./useController";
import type { ForkCreationView } from "../generated/desktopContract.generated";

/** The bridge commands a remote turn fork needs, so a caller can supply its own. */
export interface RemoteForkBindings {
  ForkTargetsRemoteTab(tabID: string): Promise<ForkTargetSetView>;
  CreateForkRemoteTab(tabID: string, turnID: string, operationID: string): Promise<ForkCreationView>;
}

/**
 * Reads a remote tab's fork boundaries, or undefined when this shell cannot ask.
 * The serve derives them from its own committed turn records, so the read is
 * valid while a turn runs, and an empty set means "no completed turn here" —
 * never "this serve cannot fork", which its advertised capability decides.
 */
export async function readRemoteForkTargets(bindings: RemoteForkBindings, tabId: string): Promise<ForkTargetSetView | undefined> {
  // A shell that predates the command reads no targets instead of failing the
  // transcript around it.
  if (typeof bindings.ForkTargetsRemoteTab !== "function") return undefined;
  const set = await bindings.ForkTargetsRemoteTab(tabId).catch(() => undefined);
  if (!set) return undefined;
  return { targets: Array.isArray(set.targets) ? set.targets : [], verifiable: Boolean(set.verifiable) };
}

/** What one remote fork request produced: the child's id, or the refusal to show the user. */
export type RemoteForkOutcome = { kind: "created"; sessionId: string } | { kind: "refused"; failure: string };

/**
 * Creates or reopens the child for one remote turn: a child this tab already
 * published is reopened by name, and a refusal carries the serve's own words.
 */
export async function requestRemoteForkTurn(
  bindings: RemoteForkBindings,
  tabId: string,
  turnId: string,
  rememberedChild?: string,
): Promise<RemoteForkOutcome> {
  if (rememberedChild) return { kind: "created", sessionId: rememberedChild };
  try {
    // A fresh operation id per user action: the serve reuses the child a
    // repeated request names, so a retry cannot publish a second one.
    const created = await bindings.CreateForkRemoteTab(tabId, turnId, crypto.randomUUID());
    if (created?.sessionId) return { kind: "created", sessionId: created.sessionId };
    // The refusal and the unsupported answer both arrive as the host's own
    // words; a reason token it names becomes the localized reason.
    const detail = created?.error?.trim() ?? "";
    return { kind: "refused", failure: detail ? forkCreateFailureText(detail) : t("chat.branchFailed") };
  } catch (error) {
    return { kind: "refused", failure: forkCreateFailureText(error) };
  }
}

/**
 * The children one remote session published, kept with the identity of the
 * session itself. The tab is reused by the child it opens, so the session that
 * published a child is the only one allowed to reopen it: a child inherits its
 * parent's turn ids verbatim, and a memory kept across that switch answers a
 * fork of turn X with a child of the session the tab no longer shows.
 */
export interface RemoteForkChildren {
  /** The tab's session identity while `byTurn` was recorded. */
  session: string;
  byTurn: Record<string, string>;
}

/** No children are reusable from another session, so the empty record is shared. */
const NO_FORK_CHILDREN: Record<string, string> = {};

/**
 * Records a child one session published, keeping the first id it recorded for
 * the turn: a repeat fork reopens that child instead of creating a second one.
 * A record from another session is replaced rather than extended, so the memory
 * never mixes two sessions' children.
 */
export function rememberForkChild(memory: RemoteForkChildren, session: string, turnId: string, sessionId: string): RemoteForkChildren {
  if (memory.session !== session) return { session, byTurn: { [turnId]: sessionId } };
  if (memory.byTurn[turnId] === sessionId) return memory;
  return { session, byTurn: { ...memory.byTurn, [turnId]: sessionId } };
}

/** The fork state one remote session owns: the child published per turn, and the read of the serve's boundaries. */
export interface RemoteForkTurnApi {
  /** Creates the child for one turn, or reopens the one this tab published; undefined leaves the refusal to the caller. */
  forkTurn: (turnId: string) => Promise<string | undefined>;
  /** Keeps a child whose surface did not open, so a repeat fork reopens it instead of creating another. */
  rememberUnopenedFork: (turnId: string, sessionId: string) => void;
  /** Forgets every child, for a tab whose connection was replaced entirely. */
  resetForkChildren: () => void;
  /**
   * The read the connection effect runs once hydration lands and once a turn
   * finishes. That effect owns its own install and uninstall per connection
   * generation, so it reaches the read through this ref rather than closing
   * over a callback identity it cannot depend on.
   */
  forkTargetsRefreshRef: RefObject<(() => Promise<void>) | null>;
}

/**
 * Owns one remote session's fork state: the children this tab already published
 * on the serve, keyed by their turn, and the read that folds the serve's fork
 * boundaries into the transcript through the shared reducer.
 *
 * sessionPath is the identity of the session the tab currently shows. The tab
 * is reused when a fork navigates it to the child, so the memory is scoped to
 * that identity instead of the tab: it is read only while the session that
 * published it is still the one on screen.
 */
export function useRemoteForkTurn(
  bindings: RemoteForkBindings,
  tabId: string | undefined,
  sessionPath: string | undefined,
  setTranscript: (update: (state: State) => State) => void,
  setPromptError: (error: string) => void,
): RemoteForkTurnApi {
  const session = sessionPath ?? "";
  const [forkChildren, setForkChildren] = useState<RemoteForkChildren>(() => ({ session, byTurn: {} }));
  const published = forkChildren.session === session ? forkChildren.byTurn : NO_FORK_CHILDREN;
  const forkTargetsRefreshRef = useRef<(() => Promise<void>) | null>(null);
  const refreshForkTargets = useCallback(async (): Promise<void> => {
    if (!tabId) return;
    const targets = await readRemoteForkTargets(bindings, tabId);
    if (!targets) return;
    setTranscript((current) => reducer(current, { type: "fork_targets", targets }));
  }, [bindings, setTranscript, tabId]);
  useEffect(() => { forkTargetsRefreshRef.current = refreshForkTargets; }, [refreshForkTargets]);

  const forkTurn = useCallback(async (turnId: string): Promise<string | undefined> => {
    if (!tabId || !turnId) return undefined;
    setPromptError("");
    const outcome = await requestRemoteForkTurn(bindings, tabId, turnId, published[turnId]);
    if (outcome.kind === "created") return outcome.sessionId;
    setPromptError(outcome.failure);
    return undefined;
  }, [bindings, published, setPromptError, tabId]);

  const rememberUnopenedFork = useCallback(
    (turnId: string, sessionId: string) => setForkChildren((current) => rememberForkChild(current, session, turnId, sessionId)),
    [session],
  );
  const resetForkChildren = useCallback(
    () => setForkChildren((current) => Object.keys(current.byTurn).length === 0 ? current : { ...current, byTurn: {} }),
    [],
  );
  return { forkTurn, rememberUnopenedFork, resetForkChildren, forkTargetsRefreshRef };
}
