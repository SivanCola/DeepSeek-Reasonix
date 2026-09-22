import type { Meta, TabMeta } from "./types";
import { sessionIdentityStableKey } from "./sessionIdentity";

/** A passive runtime refresh may retain an already certified reading window.
 * Missing content metadata is not an instruction to replace it with the tail.
 * Navigation, generation changes, explicit retry and new content proof still
 * require the reader to establish a fresh window. */
export function coldHistoryRefreshProof(target: TabMeta, state: {
  meta?: Meta; hydrating?: boolean; hydrateError?: string;
  historyRevision?: number; historyDigest?: string;
} | undefined, preserve: boolean): { revision?: number; digest: string } | undefined {
  if (!preserve || !state || state.hydrating || state.hydrateError || !state.historyDigest
    || sessionIdentityStableKey(target) !== sessionIdentityStableKey(state.meta)) return undefined;
  if (target.sessionDigest && target.sessionDigest !== state.historyDigest) return undefined;
  if (target.sessionRevision !== undefined && target.sessionRevision > 0 && target.sessionRevision !== state.historyRevision) return undefined;
  return { revision: state.historyRevision, digest: state.historyDigest };
}
