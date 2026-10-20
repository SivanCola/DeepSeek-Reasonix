import { useSyncExternalStore } from "react";

const KEY = "rx-new-answer-marker";
const prefListeners = new Set<() => void>();

export function showsNewAnswerMarker(): boolean {
  try {
    return localStorage.getItem(KEY) !== "off";
  } catch {
    return true;
  }
}

export function setShowsNewAnswerMarker(on: boolean): void {
  try {
    localStorage.setItem(KEY, on ? "on" : "off");
  } catch {
    /* the choice holds for this window and is forgotten on the next */
  }
  prefListeners.forEach((fn) => fn());
}

export function onNewAnswerMarkerChange(fn: () => void): () => void {
  prefListeners.add(fn);
  const storage = (event: StorageEvent) => {
    if (event.key === KEY || event.key === null) fn();
  };
  window.addEventListener("storage", storage);
  return () => {
    prefListeners.delete(fn);
    window.removeEventListener("storage", storage);
  };
}

export const sessionKey = (host: string | undefined, path: string) => `${host ?? ""}\n${path}`;

// When the person last looked, per conversation, taken the moment its unread
// mark is cleared and kept until the next unread span replaces it. Memory only:
// the kernel keeps the mark, not where it began.
const since = new Map<string, number>();
const sinceListeners = new Set<() => void>();
const sinceChanged = () => sinceListeners.forEach((fn) => fn());

export function rememberUnreadSince(key: string, at: number): void {
  if (since.get(key) === at) return;
  since.set(key, at);
  sinceChanged();
}

export function useUnreadSince(key: string | undefined): number | undefined {
  const read = () => (key === undefined ? undefined : since.get(key));
  return useSyncExternalStore(
    (fn) => {
      sinceListeners.add(fn);
      return () => {
        sinceListeners.delete(fn);
      };
    },
    read,
    read,
  );
}
