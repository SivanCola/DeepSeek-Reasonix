import { create } from "zustand";

import { app } from "../lib/bridge";
import type { TerminalSessionView, TerminalWorkspaceView } from "../lib/types";
import { registerTerminalExitListener } from "../lib/terminalEvents";

type TerminalState = {
  tabId: string;
  generation: number;
  workspace: TerminalWorkspaceView | null;
  loading: boolean;
  activeSessionId: string | null;
  syncWorkspace: (tabId: string) => Promise<TerminalWorkspaceView | null>;
  ensureReady: (tabId: string) => Promise<TerminalWorkspaceView | null>;
  createSession: (tabId: string, relativePath?: string, shellId?: string) => Promise<TerminalSessionView | null>;
  write: (tabId: string, sessionId: string, data: string) => Promise<void>;
  resize: (tabId: string, sessionId: string, cols: number, rows: number) => Promise<void>;
  closeSession: (tabId: string, sessionId: string) => Promise<void>;
  renameSession: (tabId: string, sessionId: string, title: string) => Promise<void>;
  setActiveSession: (sessionId: string | null) => void;
};

let inFlight: { tabId: string; promise: Promise<TerminalWorkspaceView | null> } | null = null;

function normalizedWorkspace(value: TerminalWorkspaceView): TerminalWorkspaceView {
  return {
    ...value,
    sessions: Array.isArray(value.sessions) ? value.sessions : [],
    shells: Array.isArray(value.shells) ? value.shells : [],
  };
}

export const useTerminalStore = create<TerminalState>((set, get) => ({
  tabId: "",
  generation: 0,
  workspace: null,
  loading: false,
  activeSessionId: null,
  async syncWorkspace(tabId) {
    const normalizedTabId = tabId.trim();
    if (!normalizedTabId) {
      set({ tabId: "", workspace: null, activeSessionId: null, loading: false });
      return null;
    }
    const generation = get().generation + 1;
    set({ tabId: normalizedTabId, generation, loading: true, workspace: null, activeSessionId: null });
    const request = app.TerminalWorkspaceForTab(normalizedTabId)
      .then((value) => {
        const workspace = normalizedWorkspace(value);
        const current = get();
        if (current.tabId !== normalizedTabId || current.generation !== generation) return null;
        const active = workspace.sessions.find((session) => session.running)?.id ?? workspace.sessions[0]?.id ?? null;
        set({ workspace, loading: false, activeSessionId: active });
        return workspace;
      })
      .catch((error) => {
        const current = get();
        if (current.tabId === normalizedTabId && current.generation === generation) {
          set({ workspace: null, loading: false, activeSessionId: null });
        }
        throw error;
      });
    inFlight = { tabId: normalizedTabId, promise: request };
    try {
      return await request;
    } finally {
      if (inFlight?.promise === request) inFlight = null;
    }
  },
  async ensureReady(tabId) {
    const normalizedTabId = tabId.trim();
    const current = get();
    if (current.tabId === normalizedTabId && current.workspace && !current.loading) return current.workspace;
    if (inFlight?.tabId === normalizedTabId) return inFlight.promise;
    return get().syncWorkspace(normalizedTabId);
  },
  async createSession(tabId, relativePath = ".", shellId = "default") {
    const workspace = await get().ensureReady(tabId);
    if (!workspace?.available || workspace.readOnly) return null;
    const session = await app.CreateTerminalForTab(tabId, relativePath, shellId);
    const current = get();
    if (current.tabId !== tabId) return session;
    const next = { ...workspace, sessions: [...workspace.sessions, session] };
    set({ workspace: next, activeSessionId: session.id });
    return session;
  },
  async write(tabId, sessionId, data) {
    await app.WriteTerminalForTab(tabId, sessionId, data);
  },
  async resize(tabId, sessionId, cols, rows) {
    await app.ResizeTerminalForTab(tabId, sessionId, cols, rows);
  },
  async closeSession(tabId, sessionId) {
    await app.CloseTerminalForTab(tabId, sessionId);
    const current = get();
    if (current.tabId !== tabId || !current.workspace) return;
    const sessions = current.workspace.sessions.filter((session) => session.id !== sessionId);
    set({ workspace: { ...current.workspace, sessions }, activeSessionId: sessions[0]?.id ?? null });
  },
  async renameSession(tabId, sessionId, title) {
    await app.RenameTerminalForTab(tabId, sessionId, title);
    const current = get();
    if (current.tabId !== tabId || !current.workspace) return;
    const sessions = current.workspace.sessions.map((session) => session.id === sessionId ? { ...session, title } : session);
    set({ workspace: { ...current.workspace, sessions } });
  },
  setActiveSession: (activeSessionId) => set({ activeSessionId }),
}));

registerTerminalExitListener((event) => {
  const current = useTerminalStore.getState();
  if (!current.workspace) return;
  const sessions = current.workspace.sessions.map((session) => session.id === event.id
    ? { ...session, running: false, exitCode: event.exitCode }
    : session);
  useTerminalStore.setState({ workspace: { ...current.workspace, sessions } });
});

export function resetTerminalStoreForTests(): void {
  inFlight = null;
  useTerminalStore.setState({ tabId: "", generation: 0, workspace: null, loading: false, activeSessionId: null });
}
