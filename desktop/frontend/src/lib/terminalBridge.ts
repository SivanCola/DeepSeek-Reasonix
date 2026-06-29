import { useEffect } from "react";

export const TERMINAL_OUTPUT_CHANNEL = "terminal:output";
export const TERMINAL_EXIT_CHANNEL = "terminal:exit";

export type TerminalOutputEvent = { id: string; data: string };
export type TerminalExitEvent = { id: string; exitCode: number };

function parsePayload<T>(payload: unknown): T | null {
  if (payload == null) return null;
  if (typeof payload === "object") return payload as T;
  return null;
}

const mockTerminalOutputListeners = new Set<(event: TerminalOutputEvent) => void>();
const mockTerminalExitListeners = new Set<(event: TerminalExitEvent) => void>();

export function notifyMockTerminalOutput(event: TerminalOutputEvent): void {
  mockTerminalOutputListeners.forEach((cb) => cb(event));
}

export function notifyMockTerminalExit(event: TerminalExitEvent): void {
  mockTerminalExitListeners.forEach((cb) => cb(event));
}

export function onTerminalOutput(cb: (event: TerminalOutputEvent) => void): () => void {
  mockTerminalOutputListeners.add(cb);
  const offRuntime =
    typeof window !== "undefined" && window.runtime
      ? window.runtime.EventsOn(TERMINAL_OUTPUT_CHANNEL, (payload) => {
          const event = parsePayload<TerminalOutputEvent>(payload);
          if (event?.id && typeof event.data === "string") cb(event);
        })
      : () => {};
  return () => {
    mockTerminalOutputListeners.delete(cb);
    offRuntime();
  };
}

export function onTerminalExit(cb: (event: TerminalExitEvent) => void): () => void {
  mockTerminalExitListeners.add(cb);
  const offRuntime =
    typeof window !== "undefined" && window.runtime
      ? window.runtime.EventsOn(TERMINAL_EXIT_CHANNEL, (payload) => {
          const event = parsePayload<TerminalExitEvent>(payload);
          if (event?.id && typeof event.exitCode === "number") cb(event);
        })
      : () => {};
  return () => {
    mockTerminalExitListeners.delete(cb);
    offRuntime();
  };
}

export function decodeTerminalOutput(data: string): string {
  try {
    const binary = atob(data);
    const bytes = Uint8Array.from(binary, (c) => c.charCodeAt(0));
    return new TextDecoder("utf-8", { fatal: false }).decode(bytes);
  } catch {
    return "";
  }
}

export function useTerminalOutput(sessionId: string | null, onData: (text: string) => void): void {
  useEffect(() => {
    if (!sessionId) return;
    return onTerminalOutput((event) => {
      if (event.id !== sessionId) return;
      onData(decodeTerminalOutput(event.data));
    });
  }, [sessionId, onData]);
}

export function useTerminalExit(sessionId: string | null, onExit: (exitCode: number) => void): void {
  useEffect(() => {
    if (!sessionId) return;
    return onTerminalExit((event) => {
      if (event.id !== sessionId) return;
      onExit(event.exitCode);
    });
  }, [sessionId, onExit]);
}

function workspaceStorageKey(prefix: string, workspaceRoot: string): string {
  let hash = 0;
  for (let i = 0; i < workspaceRoot.length; i += 1) {
    hash = (hash * 31 + workspaceRoot.charCodeAt(i)) | 0;
  }
  return `${prefix}.${Math.abs(hash).toString(36)}`;
}

export function loadTerminalActiveSession(workspaceRoot: string): string | null {
  if (typeof window === "undefined" || !workspaceRoot) return null;
  try {
    return window.localStorage.getItem(workspaceStorageKey("reasonix.terminal.activeSession", workspaceRoot));
  } catch {
    return null;
  }
}

export function saveTerminalActiveSession(workspaceRoot: string, sessionId: string | null): void {
  if (typeof window === "undefined" || !workspaceRoot) return;
  try {
    const key = workspaceStorageKey("reasonix.terminal.activeSession", workspaceRoot);
    if (!sessionId) window.localStorage.removeItem(key);
    else window.localStorage.setItem(key, sessionId);
  } catch {
    /* ignore */
  }
}
