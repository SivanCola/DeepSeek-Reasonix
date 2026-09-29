import { app, openExternal } from "../lib/bridge";
import { useCommittedCommand } from "../lib/useCommittedCommand";
import { useOverlayStore } from "../store/overlays";

/**
 * Owns the startup/session banner commands: session reclaim or takeover, the
 * takeover dialog, provider setup navigation and the
 * release-notes link. Banner state (busy tab, dialog, provider gate) lives on
 * the overlay store.
 */
export function useSessionBannerCommands(options: { remote: boolean }) {
  const reclaimBusyTab = useOverlayStore((state) => state.reclaimBusyTab);
  const setReclaimBusyTab = useOverlayStore((state) => state.setReclaimBusyTab);
  const setTakeoverDialogTab = useOverlayStore((state) => state.setTakeoverDialogTab);

  const reclaimSession = useCommittedCommand((tabId: string) => {
    if (reclaimBusyTab) return;
    setReclaimBusyTab(tabId);
    (options.remote ? app.ReclaimRemoteTabSession(tabId) : app.TakeoverSession(tabId, "wait"))
      .catch((error) => console.warn("[takeover] reclaim failed", error))
      .finally(() => setReclaimBusyTab(null));
  });

  const openTakeoverDialog = useCommittedCommand((tabId: string) => setTakeoverDialogTab(tabId));
  const closeTakeoverDialog = useCommittedCommand(() => setTakeoverDialogTab(null));

  const showReleaseNotes = useCommittedCommand((latest: string) => {
    const version = latest.replace(/^(?:desktop-)?v/, "");
    void openExternal(`https://reasonix.io/changelog/v${version}/`);
  });

  return { reclaimSession, openTakeoverDialog, closeTakeoverDialog, showReleaseNotes };
}
