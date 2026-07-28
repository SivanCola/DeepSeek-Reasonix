import type { TabMeta } from "./types";

export const TAB_META_VISIBLE_FALLBACK_MS = 15_000;
export const TAB_META_HIDDEN_FALLBACK_MS = 60_000;
export const TAB_META_MAX_IN_FLIGHT = 2;

const TAB_META_EVENT_KINDS = new Set([
  "turn_started",
  "turn_done",
  "retrying",
  "approval_request",
  "ask_request",
]);

export function tabMetaFallbackDelay(visibility: DocumentVisibilityState): number {
  return visibility === "hidden" ? TAB_META_HIDDEN_FALLBACK_MS : TAB_META_VISIBLE_FALLBACK_MS;
}

export function shouldRefreshTabMetaForEvent(kind: string): boolean {
  return TAB_META_EVENT_KINDS.has(kind);
}

export function sameTabMetaLists(current: readonly TabMeta[], next: readonly TabMeta[]): boolean {
  if (current === next) return true;
  if (current.length !== next.length) return false;
  for (let index = 0; index < current.length; index += 1) {
    if (JSON.stringify(current[index]) !== JSON.stringify(next[index])) return false;
  }
  return true;
}
