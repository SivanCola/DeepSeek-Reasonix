/** Child-window remote desktop context injected by the Go shell. */

export interface RemoteWindowChrome {
  mode: "gateway";
  hostId?: string;
  workspace?: string;
  sessionId?: string;
}

declare global {
  interface Window {
    __REASONIX_REMOTE__?: RemoteWindowChrome;
  }
}

/** True when this webview is a remote-desktop child (not the primary local app). */
export function isRemoteDesktopWindow(): boolean {
  return window.__REASONIX_REMOTE__?.mode === "gateway";
}

export function remoteDesktopHostId(): string | undefined {
  return window.__REASONIX_REMOTE__?.hostId;
}

export function remoteDesktopWorkspace(): string | undefined {
  return window.__REASONIX_REMOTE__?.workspace;
}
