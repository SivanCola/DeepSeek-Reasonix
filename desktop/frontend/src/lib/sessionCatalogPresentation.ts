import type { SessionCatalogStatus } from "./sessionCatalogTypes";

export type SessionCatalogNotice = "working" | "failed" | "rebuild";

export function sessionCatalogNotice(status: SessionCatalogStatus): SessionCatalogNotice | null {
  const working = status.state === "opening"
    || status.state === "rebuilding"
    || status.repairPending > 0
    || (status.unindexedTargetCount ?? 0) > 0;
  if (working) return "working";
  if (status.state === "degraded" || status.lastError) return status.canRebuild === true ? "rebuild" : "failed";
  return null;
}

// Local rebuild transitions own status over older asynchronous reads. A failed
// rebuild also keeps its retryable snapshot until the next rebuild clears it.
export function sessionCatalogStatusWriteIsAllowed(
  currentGeneration: number,
  candidateGeneration: number,
  rebuildFailed: boolean,
): boolean {
  return !rebuildFailed && candidateGeneration === currentGeneration;
}
