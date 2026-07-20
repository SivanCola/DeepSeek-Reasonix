/**
 * Remote AppBridge: child desktop windows in gateway mode proxy chat/session
 * control and workspace I/O through the parent Remote Gateway (loopback).
 * Tokens stay in Go/Wails IPC; the browser only uses non-secret chrome context
 * plus gateway credentials from RemoteWindowInfo.
 */

import type { TabMeta } from "./types";
import { isRemoteDesktopWindow, remoteDesktopHostId, remoteDesktopWorkspace } from "./remoteWindow";

export type RemoteGatewaySession = {
  gatewayUrl: string;
  gatewayToken: string;
  sessionId: string;
  hostId: string;
  workspace: string;
  remoteSessionId?: string;
  modelRef?: string;
  ready: boolean;
};

let cached: RemoteGatewaySession | null | undefined;
let eventsStarted = false;
let eventUnsub: (() => void) | null = null;

async function readRemoteWindowInfo(): Promise<Record<string, string> | null> {
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
      ready: true,
    };
    return cached;
  } catch {
    cached = null;
    return null;
  }
}

export function clearRemoteGatewaySessionCache(): void {
  cached = undefined;
  eventUnsub?.();
  eventUnsub = null;
  eventsStarted = false;
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

async function ensureRemoteSession(): Promise<string> {
  const sess = await loadRemoteGatewaySession();
  if (sess?.remoteSessionId) return sess.remoteSessionId;
  return remoteCreateSession();
}

async function withSession(pathSuffix: string, init?: RequestInit): Promise<Response> {
  const sid = await ensureRemoteSession();
  return gatewayFetch(`/gateway/v1/remote/sessions/${encodeURIComponent(sid)}${pathSuffix}`, init);
}

export async function remoteHello(): Promise<Record<string, unknown>> {
  const resp = await gatewayFetch("/gateway/v1/remote/hello");
  if (!resp.ok) throw new Error(`remote hello failed: ${resp.status}`);
  return resp.json() as Promise<Record<string, unknown>>;
}

export async function remoteListSessions(): Promise<{ sessions: Array<{ id: string; label?: string; modelRef?: string; running?: boolean }> }> {
  const resp = await gatewayFetch("/gateway/v1/remote/sessions");
  if (!resp.ok) throw new Error(`list sessions failed: ${resp.status}`);
  return resp.json() as Promise<{ sessions: Array<{ id: string; label?: string; modelRef?: string; running?: boolean }> }>;
}

export async function remoteCreateSession(model?: string): Promise<string> {
  const resp = await gatewayFetch("/gateway/v1/remote/sessions", {
    method: "POST",
    body: JSON.stringify({ model: model || "" }),
  });
  if (!resp.ok) throw new Error(`create session failed: ${resp.status}`);
  const body = (await resp.json()) as { session?: { id?: string; modelRef?: string } };
  const id = body.session?.id;
  if (!id) throw new Error("create session returned no id");
  const sess = await loadRemoteGatewaySession();
  if (sess) {
    sess.remoteSessionId = id;
    sess.modelRef = body.session?.modelRef;
  }
  await gatewayFetch("/gateway/v1/session/active", {
    method: "POST",
    body: JSON.stringify({ remoteSessionId: id }),
  }).catch(() => undefined);
  return id;
}

export async function remoteSubmit(input: string, display?: string): Promise<void> {
  const resp = await withSession("/submit", {
    method: "POST",
    body: JSON.stringify({ input, display: display || "" }),
  });
  if (!resp.ok && resp.status !== 202) {
    throw new Error(`submit failed: ${resp.status}`);
  }
}

export async function remoteCancel(): Promise<void> {
  const sess = await loadRemoteGatewaySession();
  if (!sess?.remoteSessionId) return;
  await withSession("/cancel", { method: "POST", body: "{}" });
}

export async function remoteApprove(id: string, allow: boolean, session: boolean, persist: boolean): Promise<void> {
  const resp = await withSession("/approve", {
    method: "POST",
    body: JSON.stringify({ id, allow, session, persist }),
  });
  if (!resp.ok) throw new Error(`approve failed: ${resp.status}`);
}

export async function remoteAnswer(id: string, answers: unknown): Promise<void> {
  const resp = await withSession("/answer", {
    method: "POST",
    body: JSON.stringify({ id, answers }),
  });
  if (!resp.ok) throw new Error(`answer failed: ${resp.status}`);
}

export async function remoteCompact(): Promise<void> {
  const resp = await withSession("/compact", { method: "POST", body: "{}" });
  if (!resp.ok) throw new Error(`compact failed: ${resp.status}`);
}

export async function remoteRewind(turn: number, scope?: string): Promise<void> {
  const resp = await withSession("/rewind", {
    method: "POST",
    body: JSON.stringify({ turn, scope: scope || "both" }),
  });
  if (!resp.ok) throw new Error(`rewind failed: ${resp.status}`);
}

export async function remoteSetModel(model: string, effort?: string | null): Promise<void> {
  const body: { model: string; effort?: string } = { model };
  if (effort != null) body.effort = effort;
  const resp = await withSession("/model", { method: "POST", body: JSON.stringify(body) });
  if (!resp.ok) throw new Error(`set model failed: ${resp.status}`);
  const sess = await loadRemoteGatewaySession();
  if (sess) sess.modelRef = model;
}

/** Synthetic single tab for the remote workspace (remote child has no local tabs). */
export async function remoteListTabs(): Promise<TabMeta[]> {
  const sess = await loadRemoteGatewaySession();
  if (!sess) return [];
  let remoteId = sess.remoteSessionId;
  let model = sess.modelRef || "";
  let running = false;
  try {
    const listed = await remoteListSessions();
    if (listed.sessions?.length) {
      const match = remoteId
        ? listed.sessions.find((s) => s.id === remoteId)
        : listed.sessions[0];
      if (match) {
        remoteId = match.id;
        model = match.modelRef || model;
        running = !!match.running;
        sess.remoteSessionId = remoteId;
      }
    }
  } catch {
    // still return a shell tab so the UI can recover
  }
  if (!remoteId) {
    try {
      remoteId = await remoteCreateSession();
    } catch {
      remoteId = "remote-pending";
    }
  }
  const host = sess.hostId || "remote";
  const ws = sess.workspace || "";
  const name = ws.split("/").filter(Boolean).pop() || host;
  return [
    {
      id: remoteId,
      scope: "project",
      workspaceRoot: ws,
      workspaceName: name,
      workspacePath: ws,
      topicId: "remote",
      topicTitle: `${host}:${name}`,
      label: model || "remote",
      ready: true,
      running,
      cancellable: running,
      mode: "normal",
      toolApprovalMode: "ask",
      tokenMode: "full",
      active: true,
      cwd: ws,
      executionTarget: {
        kind: "ssh",
        hostId: sess.hostId,
        workspace: sess.workspace,
      },
      remoteSessionId: remoteId,
      remoteHost: sess.hostId,
      remoteWorkspace: sess.workspace,
      brokerStatus: "ready",
    },
  ];
}

export async function remoteListDir(path: string): Promise<unknown[]> {
  const sess = await loadRemoteGatewaySession();
  const q = new URLSearchParams({ path: path || sess?.workspace || "" });
  const resp = await gatewayFetch(`/gateway/v1/fs/list?${q}`);
  if (!resp.ok) throw new Error(`list dir failed: ${resp.status}`);
  const body = (await resp.json()) as { entries?: unknown[] };
  return body.entries ?? [];
}

export async function remoteReadFile(path: string): Promise<unknown> {
  const q = new URLSearchParams({ path });
  const resp = await gatewayFetch(`/gateway/v1/fs/read?${q}`);
  if (!resp.ok) throw new Error(`read file failed: ${resp.status}`);
  return resp.json();
}

export async function remoteWriteFile(path: string, body: string, expectMtime = 0): Promise<unknown> {
  const resp = await gatewayFetch("/gateway/v1/fs/write", {
    method: "POST",
    body: JSON.stringify({ path, body, expectMtime }),
  });
  if (!resp.ok) throw new Error(`write file failed: ${resp.status}`);
  return resp.json();
}

export async function remoteGitStatus(): Promise<string> {
  const resp = await gatewayFetch("/gateway/v1/git/status");
  if (!resp.ok) throw new Error(`git status failed: ${resp.status}`);
  const body = (await resp.json()) as { status?: string };
  return body.status ?? "";
}

export async function remoteGitDiff(): Promise<string> {
  const resp = await gatewayFetch("/gateway/v1/git/diff");
  if (!resp.ok) throw new Error(`git diff failed: ${resp.status}`);
  const body = (await resp.json()) as { diff?: string };
  return body.diff ?? "";
}

/**
 * Subscribe to remote SSE and forward frames as agent:event-shaped WireEvents
 * when possible so useController can render the stream.
 */
export async function ensureRemoteEventPump(
  emit: (e: Record<string, unknown>) => void,
): Promise<() => void> {
  if (eventsStarted) return () => {};
  eventsStarted = true;
  const unsub = await remoteSubscribeEvents((raw) => {
    if (!raw || typeof raw !== "object") return;
    const env = raw as { kind?: string; payload?: unknown; sessionId?: string };
    // Session/checkpoint notices become UI notices; wire events carry payload.kind.
    if (env.kind === "checkpoint" || env.kind === "session" || env.kind === "notice") {
      emit({
        kind: "notice",
        level: "info",
        text: env.kind === "checkpoint" ? "Remote session checkpoint" : String(env.kind),
        tabId: (raw as { sessionId?: string }).sessionId,
      });
      return;
    }
    if (env.payload && typeof env.payload === "object") {
      const wire = env.payload as Record<string, unknown>;
      emit({ ...wire, tabId: env.sessionId || wire.tabId });
      return;
    }
    emit({ kind: env.kind || "notice", tabId: env.sessionId });
  });
  eventUnsub = unsub;
  return unsub;
}

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
                // drop
              }
            }
          }
        }
      }
    } catch {
      // aborted
    }
  })();
  return () => ctrl.abort();
}

export async function shouldUseRemoteAppBridge(): Promise<boolean> {
  if (!isRemoteDesktopWindow()) return false;
  return (await loadRemoteGatewaySession()) != null;
}

/** Dispatch a bridge method to remote handlers; null means fall through. */
export function dispatchRemoteBridgeMethod(method: string, args: unknown[]): Promise<unknown> | null {
  if (typeof window === "undefined" || window.__REASONIX_REMOTE__?.mode !== "gateway") {
    return null;
  }
  switch (method) {
    case "Submit":
    case "SubmitToTab":
      return remoteSubmit(String(args[method === "Submit" ? 0 : 1] ?? ""));
    case "SubmitDisplay":
      return remoteSubmit(String(args[1] ?? ""), String(args[0] ?? ""));
    case "SubmitDisplayToTab":
      return remoteSubmit(String(args[2] ?? ""), String(args[1] ?? ""));
    case "SubmitEditedDisplayToTab":
      return remoteSubmit(String(args[2] ?? ""), String(args[1] ?? ""));
    case "SubmitDeliveryRecoveryToTab":
      return remoteSubmit(String(args[2] ?? ""), String(args[1] ?? ""));
    case "SubmitInvocationsToTab":
      return remoteSubmit(String(args[2] ?? ""), String(args[1] ?? ""));
    case "Cancel":
    case "CancelTab":
      return remoteCancel();
    case "Approve":
      return remoteApprove(String(args[0] ?? ""), !!args[1], !!args[2], !!args[3]);
    case "ApproveTab":
      return remoteApprove(String(args[1] ?? ""), !!args[2], !!args[3], !!args[4]);
    case "AnswerQuestion":
      return remoteAnswer(String(args[0] ?? ""), args[1]);
    case "AnswerQuestionForTab":
      return remoteAnswer(String(args[1] ?? ""), args[2]);
    case "Compact":
    case "CompactForTab":
      return remoteCompact();
    case "Rewind":
    case "RewindForTab":
      return remoteRewind(Number(args[method === "Rewind" ? 0 : 1] ?? 0), String(args[method === "Rewind" ? 1 : 2] ?? "both"));
    case "SetModel":
      return remoteSetModel(String(args[0] ?? ""));
    case "SetModelForTab":
      return remoteSetModel(String(args[1] ?? ""));
    case "SetEffort":
    case "SetEffortForTab":
      // Effort-only switch needs a full model rebuild on remote; no-op until the
      // child tracks the active model ref for a combined model+effort call.
      return Promise.resolve();
    case "ListTabs":
      return remoteListTabs();
    case "ListRemoteDir":
      return remoteListDir(String(args[1] ?? args[0] ?? ""));
    case "ReadRemoteFile":
      return remoteReadFile(String(args[1] ?? args[0] ?? ""));
    case "WriteRemoteFile":
      return remoteWriteFile(String(args[1] ?? ""), String(args[2] ?? ""), Number(args[3] ?? 0));
    case "IsRemoteWindow":
      return Promise.resolve(true);
    // Local-only surfaces: no-op safely in remote windows.
    case "SetMode":
    case "SetModeForTab":
    case "SetCollaborationMode":
    case "SetCollaborationModeForTab":
    case "SetToolApprovalMode":
    case "SetToolApprovalModeForTab":
    case "SetTokenMode":
    case "SetTokenModeForTab":
    case "SetPlanMode":
    case "SetAutoApproveTools":
    case "Steer":
    case "SteerForTab":
    case "RunShell":
    case "RunShellForTab":
    case "ReplayPendingPrompts":
      return Promise.resolve(method.startsWith("SetMode") || method.includes("ToolApproval") ? [] : undefined);
    default:
      return null;
  }
}
