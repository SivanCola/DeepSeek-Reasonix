export function resolveTurnStartedAt(current: number, reported: number | undefined, now = Date.now()): number {
  if (typeof reported === "number" && Number.isFinite(reported) && reported > 0 && reported <= now) return reported;
  if (Number.isFinite(current) && current > 0 && current <= now) return current;
  return now;
}
