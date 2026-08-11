export function guidanceNeedsRetry(state?: string): boolean {
  return state === "uncertain" || state === "blocked";
}

export function markGuidanceQueued<T extends { id: string; state?: string; paused?: boolean }>(items: T[], id: string): T[] {
  return items.map((item) => item.id === id ? { ...item, state: "queued", paused: false } : item);
}

// Exact equality avoids consuming a different queued item whose text merely
// contains the accepted steer (#6238).
export function guidanceTextMatches(queued: string, consumed: string): boolean {
  const left = queued.trim();
  const right = consumed.trim();
  return Boolean(left && right && left === right);
}
