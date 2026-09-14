import { app } from "./bridge";
import type { TranscriptProjection } from "./transcriptStore";
import type { Meta } from "./types";

// Protocol 7 hosts use bounded recent/history reads. The snapshot client is
// retained only for older hosts that lack the new reader capability.
export function usesLegacyTranscriptSnapshots(): boolean {
  return typeof app.SessionOpenForTab !== "function" && typeof app.TranscriptSnapshotForTab === "function";
}

export function historyFingerprintMatchesMeta(history: { revision: number; revisionKnown?: boolean; digest?: string }, meta: Meta): boolean {
  const expectedDigest = (meta.sessionDigest ?? "").trim();
  if (expectedDigest && history.digest !== expectedDigest) return false;
  const expectedRevision = meta.sessionRevision ?? 0;
  return expectedRevision <= 0 || Boolean(history.revisionKnown && history.revision === expectedRevision);
}

export function historyRevisionIsOlder(current: number | undefined, incoming: number | undefined): boolean {
  return typeof current === "number" && current > 0
    && typeof incoming === "number" && incoming > 0
    && incoming < current;
}

export function historyReplaceAction(projection: TranscriptProjection) {
  return {
    type: "history_replace" as const, items: projection.items,
    startTurn: projection.startTurn, endTurn: projection.endTurn, totalTurns: projection.totalTurns,
    hasOlder: projection.hasOlder, hasNewer: projection.hasNewer,
    revision: projection.revisionKnown ? projection.revision : undefined,
    digest: projection.digest || undefined,
  };
}
