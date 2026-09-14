import { useCallback, useLayoutEffect, useSyncExternalStore } from "react";
import {
  fileNavigationKey,
  type FileNavigationOwner,
  type FileNavigationScope,
  type FileNavigationSnapshot,
} from "../lib/fileNavigationOwner";

/**
 * Read the committed navigation of one dock instance. Reading never starts a
 * navigation: the panel renders what a command already decided, so a remount,
 * a StrictMode replay or a parent re-render cannot open a file on its own.
 */
export function useFileNavigationRecord(
  owner: FileNavigationOwner,
  scope: FileNavigationScope,
  scopeKey: string,
): FileNavigationSnapshot | null {
  const key = fileNavigationKey(scope);
  const subscribe = useCallback((listener: () => void) => owner.subscribe(listener), [owner]);
  const read = useCallback(() => owner.getSnapshot(key), [owner, key]);
  const snapshot = useSyncExternalStore(subscribe, read, read);
  const { sessionTabId, dockTabId } = scope;
  useLayoutEffect(
    () => { owner.bindScope({ sessionTabId, dockTabId }, scopeKey); },
    [dockTabId, owner, scopeKey, sessionTabId],
  );
  return snapshot;
}
