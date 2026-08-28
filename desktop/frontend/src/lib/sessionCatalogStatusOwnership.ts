// Local rebuild transitions are authoritative over status reads that were
// already in flight. The reads may finish, but they cannot undo the local
// success/failure state that settled after they started. A failed rebuild also
// keeps its retryable snapshot until the next explicit rebuild clears it.
export function sessionCatalogStatusWriteIsAllowed(
  currentGeneration: number,
  candidateGeneration: number,
  rebuildFailed: boolean,
): boolean {
  return !rebuildFailed && candidateGeneration === currentGeneration;
}
