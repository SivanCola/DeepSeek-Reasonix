/**
 * Remote AppBridge: when the desktop child window runs in gateway mode, chat
 * and session commands are proxied to the parent Remote Gateway (loopback),
 * which forwards to remote-runtime. Tokens stay in Go/Wails IPC memory; the
 * browser only holds the gateway base URL + session id from window chrome.
 */

import { isRemoteDesktopWindow, remoteDesktopHostId, remoteDesktopWorkspace } from "./remoteWindow";

export type RemoteGatewaySession = {
  gatewayUrl: string;
  gatewayToken: string;
  sessionId: string;
  hostId: string;
  workspace: string;
  remoteSessionId?: string;
};

let cached: RemoteGatewaySession | null | undefined;

async function readRemoteWindowInfo(): Promise<Record<string, string> | null> {
  // Call Wails bindings directly to avoid a circular import with bridge.ts.
  const goApp = typeof window !== "undefined" ? window.go?.main?.App : undefined;
  if (!goApp || typeof goApp.RemoteWindowInfo !== "function") return null;
  return goApp.RemoteWindowInfo();
}

/** Load gateway binding once per child-window lifetime. */
export async function loadRemoteGatewaySession(): Promise<RemoteGatewaySession | null> {
  if (cached !== undefined) return cached;
  if (!isRemoteDesktopWindow()) {
    cached = null;
    return null;
  }
  try {
    const info = await readRemoteWindowInfo();
    if (!info || info.mode !== "gateway" || !info.gatewayUrl || !info.gatewayToken || !info.sessionId) {
      cached = null;
      return null;
    }
    cached = {
      gatewayUrl: info.gatewayUrl.replace(/\/$/, ""),
      gatewayToken: info.gatewayToken,
      sessionId: info.sessionId,
      hostId: info.hostId || remoteDesktopHostId() || "",
      workspace: info.workspace || remoteDesktopWorkspace() || "",
    };
    return cached;
  } catch {
    cached = null;
    return null;
  }
}

export function clearRemoteGatewaySessionCache(): void {
  cached = undefined;
}

async function gatewayFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const sess = await loadRemoteGatewaySession();
  if (!sess) throw new Error("not a remote desktop window");
  const headers = new Headers(init.headers);
  headers.set("X-Reasonix-Gateway-Token", sess.gatewayToken);
  headers.set("X-Reasonix-Session-Id", sess.sessionId);
  if (init.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  return fetch(`${sess.gatewayUrl}${path}`, { ...init, headers });
}

/** Hello against remote-runtime via gateway proxy. */
export async function remoteHello(): Promise<Record<string, unknown>> {
  const resp = await gatewayFetch("/gateway/v1/remote/hello");
  if (!resp.ok) throw new Error(`remote hello failed: ${resp.status}`);
  return resp.json() as Promise<Record<string, unknown>>;
}

/** List remote sessions. */
export async function remoteListSessions(): Promise<{ sessions: Array<{ id: string; label?: string; modelRef?: string }> }> {
  const resp = await gatewayFetch("/gateway/v1/remote/sessions");
  if (!resp.ok) throw new Error(`list sessions failed: ${resp.status}`);
  return resp.json() as Promise<{ sessions: Array<{ id: string; label?: string; modelRef?: string }> }>;
}

/** Create a remote session and remember its id. */
export async function remoteCreateSession(model?: string): Promise<string> {
  const resp = await gatewayFetch("/gateway/v1/remote/sessions", {
    method: "POST",
    body: JSON.stringify({ model: model || "" }),
  });
  if (!resp.ok) throw new Error(`create session failed: ${resp.status}`);
  const body = (await resp.json()) as { session?: { id?: string } };
  const id = body.session?.id;
  if (!id) throw new Error("create session returned no id");
  const sess = await loadRemoteGatewaySession();
  if (sess) sess.remoteSessionId = id;
  return id;
}

/** Submit a turn to the active remote session. */
export async function remoteSubmit(input: string, display?: string): Promise<void> {
  const sess = await loadRemoteGatewaySession();
  if (!sess?.remoteSessionId) {
    await remoteCreateSession();
  }
  const active = await loadRemoteGatewaySession();
  const sid = active?.remoteSessionId;
  if (!sid) throw new Error("no remote session");
  const resp = await gatewayFetch(`/gateway/v1/remote/sessions/${encodeURIComponent(sid)}/submit`, {
    method: "POST",
    body: JSON.stringify({ input, display: display || "" }),
  });
  if (!resp.ok && resp.status !== 202) {
    throw new Error(`submit failed: ${resp.status}`);
  }
}

/** Cancel the active remote turn. */
export async function remoteCancel(): Promise<void> {
  const sess = await loadRemoteGatewaySession();
  if (!sess?.remoteSessionId) return;
  await gatewayFetch(`/gateway/v1/remote/sessions/${encodeURIComponent(sess.remoteSessionId)}/cancel`, {
    method: "POST",
    body: "{}",
  });
}

/** Subscribe to remote SSE events; returns an unsubscribe function. */
export async function remoteSubscribeEvents(onEvent: (raw: unknown) => void): Promise<() => void> {
  const sess = await loadRemoteGatewaySession();
  if (!sess) return () => {};
  const ctrl = new AbortController();
  void (async () => {
    try {
      const resp = await gatewayFetch("/gateway/v1/remote/events", { signal: ctrl.signal });
      if (!resp.ok || !resp.body) return;
      const reader = resp.body.getReader();
      const decoder = new TextDecoder();
      let buf = "";
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        const parts = buf.split("\n\n");
        buf = parts.pop() ?? "";
        for (const block of parts) {
          for (const line of block.split("\n")) {
            if (line.startsWith("data: ")) {
              try {
                onEvent(JSON.parse(line.slice(6)));
              } catch {
                // drop malformed frames
              }
            }
          }
        }
      }
    } catch {
      // aborted or network error
    }
  })();
  return () => ctrl.abort();
}

/** True when submit/cancel should use the remote gateway path. */
export async function shouldUseRemoteAppBridge(): Promise<boolean> {
  if (!isRemoteDesktopWindow()) return false;
  const sess = await loadRemoteGatewaySession();
  return sess != null;
}
