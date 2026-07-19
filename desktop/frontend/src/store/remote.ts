// remote owns the transient state of the Remote-SSH surfaces: per-host
// connection status, forward snapshots, server-bootstrap state, the pending
// host-key fingerprint that drives the global confirm dialog, and the explorer
// drawer's open/host/tab selection. None of it is persisted — the kernel is the
// source of truth and hydrates it via RemoteConnectionStatuses on mount plus
// remote:* events thereafter.

import { create } from "zustand";

import type {
  RemoteConnectionStatus,
  RemoteFingerprintView,
  RemoteForwardView,
  RemoteServerView,
} from "../lib/types";

export type RemoteExplorerTab = "files" | "ports" | "server";

export type RemoteState = {
  statuses: Record<string, RemoteConnectionStatus>;
  forwards: Record<string, RemoteForwardView[]>;
  servers: Record<string, RemoteServerView>;
  pendingFingerprint: RemoteFingerprintView | null;
  explorerOpen: boolean;
  explorerHostId: string | null;
  explorerTab: RemoteExplorerTab;

  applyStatus: (s: RemoteConnectionStatus) => void;
  setStatuses: (list: RemoteConnectionStatus[]) => void;
  hydrateStatuses: (list: RemoteConnectionStatus[]) => void;
  setForwards: (hostId: string, forwards: RemoteForwardView[]) => void;
  setServer: (s: RemoteServerView) => void;
  clearPendingFingerprint: (expected?: RemoteFingerprintView) => void;
  openExplorer: (hostId: string) => void;
  closeExplorer: () => void;
  setExplorerTab: (tab: RemoteExplorerTab) => void;
};

export const useRemoteStore = create<RemoteState>((set) => ({
  statuses: {},
  forwards: {},
  servers: {},
  pendingFingerprint: null,
  explorerOpen: false,
  explorerHostId: null,
  explorerTab: "files",

  applyStatus: (s) =>
    set((state) => {
      const next: Partial<RemoteState> = {
        statuses: { ...state.statuses, [s.hostId]: s },
      };
      if (s.state === "pending_hostkey" && s.fingerprint) {
        next.pendingFingerprint = s.fingerprint;
      } else if (state.pendingFingerprint?.hostId === s.hostId) {
        // The pending prompt for this host resolved.
        next.pendingFingerprint = null;
      }
      return next;
    }),

  setStatuses: (list) =>
    set(() => {
      const statuses: Record<string, RemoteConnectionStatus> = {};
      for (const s of list) statuses[s.hostId] = s;
      return { statuses };
    }),

  hydrateStatuses: (list) =>
    set((state) => {
      const statuses = { ...state.statuses };
      for (const s of list) {
        if (!statuses[s.hostId]) statuses[s.hostId] = s;
      }
      return { statuses };
    }),

  setForwards: (hostId, forwards) =>
    set((state) => ({ forwards: { ...state.forwards, [hostId]: forwards } })),

  setServer: (s) =>
    set((state) => ({ servers: { ...state.servers, [s.hostId]: s } })),

  clearPendingFingerprint: (expected) =>
    set((state) => {
      if (expected && (
        state.pendingFingerprint?.hostId !== expected.hostId ||
        state.pendingFingerprint?.sha256 !== expected.sha256
      )) return state;
      return { pendingFingerprint: null };
    }),

  openExplorer: (hostId) => set({ explorerOpen: true, explorerHostId: hostId }),
  closeExplorer: () => set({ explorerOpen: false }),
  setExplorerTab: (tab) => set({ explorerTab: tab }),
}));
