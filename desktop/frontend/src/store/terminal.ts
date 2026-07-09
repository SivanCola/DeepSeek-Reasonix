import { create } from "zustand";

import { app } from "../lib/bridge";
import { t } from "../lib/i18n";
import { loadTerminalActiveSession, saveTerminalActiveSession } from "../lib/terminalBridge";
import type { TerminalSessionView } from "../lib/types";

let terminalErrorHandler: ((message: string) => void) | null = null;

export function setTerminalErrorHandler(handler: ((message: string) => void) | null): void {
  terminalErrorHandler = handler;
}

function reportTerminalError(err: unknown): void {
  const message = err instanceof Error ? err.message : String(err);
  terminalErrorHandler?.(t("terminal.createFailed", { message }));
}

type TerminalState = {
  sessions: TerminalSessionView[];
  activeSessionId: string | null;
  workspaceRoot: string | null;
  cwd: string | null;
  readOnly: boolean;
  busy: boolean;
  syncWorkspace: (workspaceRoot: string | null, cwd: string | null, readOnly: boolean) => Promise<void>;
  ensureReady: () => Promise<void>;
  createSession: (title?: string, cwdOverride?: string, shellPrefer?: string) => Promise<void>;
  closeSession: (id: string) => Promise<void>;
  setActiveSession: (id: string) => void;
  renameSession: (id: string, title: string) => Promise<void>;
  refreshSession: (id: string, patch: Partial<TerminalSessionView>) => void;
};

export const useTerminalStore = create<TerminalState>((set, get) => ({
  sessions: [],
  activeSessionId: null,
  workspaceRoot: null,
  cwd: null,
  readOnly: false,
  busy: false,

  syncWorkspace: async (workspaceRoot, cwd, readOnly) => {
    const prev = get();
    const nextCwd = cwd ?? workspaceRoot;
    if (prev.workspaceRoot === workspaceRoot && prev.readOnly === readOnly && prev.cwd === nextCwd) {
      return;
    }
    if (prev.workspaceRoot !== workspaceRoot || prev.readOnly !== readOnly) {
      set({
        workspaceRoot,
        cwd: nextCwd,
        readOnly,
        sessions: [],
        activeSessionId: null,
      });
      if (!workspaceRoot) return;
      try {
        const sessions = await app.ListTerminals(workspaceRoot);
        // Another syncWorkspace/createSession call may have moved the store on
        // to a different workspace while this one was in flight — a stale
        // response must not overwrite the newer workspace's state.
        if (get().workspaceRoot !== workspaceRoot) return;
        const saved = loadTerminalActiveSession(workspaceRoot);
        const activeSessionId =
          saved && sessions.some((s) => s.id === saved) ? saved : sessions[0]?.id ?? null;
        set({ sessions, activeSessionId, cwd: nextCwd });
      } catch (err) {
        if (get().workspaceRoot !== workspaceRoot) return;
        reportTerminalError(err);
        set({ sessions: [], activeSessionId: null, cwd: nextCwd });
      }
      return;
    }
    set({ cwd: nextCwd });
  },

  ensureReady: async () => {
    const { workspaceRoot, readOnly, sessions, cwd } = get();
    if (readOnly || !workspaceRoot) return;
    if (sessions.length === 0) {
      await get().createSession(undefined, cwd ?? workspaceRoot, "login");
      return;
    }
    if (!get().activeSessionId) {
      const saved = loadTerminalActiveSession(workspaceRoot);
      const next = saved && sessions.some((s) => s.id === saved) ? saved : sessions[0]?.id ?? null;
      if (next) get().setActiveSession(next);
    }
  },

  createSession: async (title?: string, cwdOverride?: string, shellPrefer?: string) => {
    const { workspaceRoot, cwd, readOnly, busy } = get();
    if (busy || readOnly || !workspaceRoot) return;
    set({ busy: true });
    try {
      const sessionCwd = cwdOverride ?? cwd ?? workspaceRoot;
      const id = await app.CreateTerminal(workspaceRoot, sessionCwd, title ?? "", shellPrefer ?? "");
      const sessions = await app.ListTerminals(workspaceRoot);
      // The user may have switched to a different workspace while the
      // terminal was being created — the new session still exists on the
      // backend and will show up next time this workspace is revisited, but
      // it must not clobber whatever workspace is current now.
      if (get().workspaceRoot !== workspaceRoot) return;
      set({ sessions, activeSessionId: id, cwd: sessionCwd });
      saveTerminalActiveSession(workspaceRoot, id);
    } catch (err) {
      reportTerminalError(err);
    } finally {
      set({ busy: false });
    }
  },

  closeSession: async (id) => {
    const { workspaceRoot, activeSessionId } = get();
    if (!workspaceRoot) return;
    await app.CloseTerminal(id);
    const sessions = await app.ListTerminals(workspaceRoot);
    if (get().workspaceRoot !== workspaceRoot) return;
    let nextActive = activeSessionId;
    if (activeSessionId === id) {
      nextActive = sessions[0]?.id ?? null;
      saveTerminalActiveSession(workspaceRoot, nextActive);
    }
    set({ sessions, activeSessionId: nextActive });
  },

  setActiveSession: (id) => {
    const { workspaceRoot } = get();
    set({ activeSessionId: id });
    if (workspaceRoot) saveTerminalActiveSession(workspaceRoot, id);
  },

  renameSession: async (id, title) => {
    const { workspaceRoot } = get();
    if (!workspaceRoot) return;
    await app.RenameTerminal(id, title);
    const sessions = await app.ListTerminals(workspaceRoot);
    if (get().workspaceRoot !== workspaceRoot) return;
    set({ sessions });
  },

  refreshSession: (id, patch) => {
    set((s) => ({
      sessions: s.sessions.map((session) => (session.id === id ? { ...session, ...patch } : session)),
    }));
  },
}));
