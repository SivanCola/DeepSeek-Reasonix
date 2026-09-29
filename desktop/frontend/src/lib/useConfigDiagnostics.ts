import { useCallback, useEffect, useRef, useState } from "react";
import { app } from "./bridge";
import { desktopHost } from "./desktopHost";
import { onRemoteTabUpdated } from "./remoteTabEvents";
import { diagnosticDismissKey, normalizeConfigDiagnostics, type ConfigDiagnosticSnapshot } from "./configDiagnostics";

// Session-local dismissal survives reopening a conversation but never hides a
// changed problem. Keys contain only host/workspace identity and issue digests.
const dismissed = new Set<string>();

export function useConfigDiagnostics(tabId: string, bindingKey = tabId) {
  const [result, setResult] = useState<{ binding: string; snapshot: ConfigDiagnosticSnapshot | null; failed: boolean } | null>(null);
  const [, changed] = useState(0);
  const request = useRef(0);
  const liveBinding = useRef(bindingKey);
  liveBinding.current = bindingKey;
  const reload = useCallback(async () => {
    const seq = ++request.current;
    if (!tabId) return;
    let snapshot: ConfigDiagnosticSnapshot | null = null;
    try { snapshot = normalizeConfigDiagnostics(await app.ConfigDiagnostics?.(tabId)); } catch { /* unavailable, never healthy */ }
    if (seq !== request.current || liveBinding.current !== bindingKey) return;
    setResult((previous) => {
      const old = previous?.binding === bindingKey ? previous.snapshot : null;
      if (old && snapshot && old.instanceId === snapshot.instanceId && old.hostId === snapshot.hostId &&
          old.workspaceId === snapshot.workspaceId && old.revision > snapshot.revision) return previous;
      return { binding: bindingKey, snapshot, failed: snapshot === null };
    });
  }, [tabId, bindingKey]);

  useEffect(() => {
    void reload();
    const host = desktopHost();
    // Legacy payloads lack project identity: use them only to invalidate an
    // explicitly bound read, never as the current project's diagnostic data.
    const stop = host.kind !== "none" ? host.events.on("config:load-warnings", () => { void reload(); }) : () => {};
    const stopSnapshots = host.kind !== "none" ? host.events.on("config:diagnostics", raw => {
      const incoming = normalizeConfigDiagnostics(raw);
      if (!incoming || liveBinding.current !== bindingKey) return;
      setResult(previous => {
        const old = previous?.binding === bindingKey ? previous.snapshot : null;
        // A request establishes the binding/instance. Events may update that
        // identity, but cannot establish a different host, project or service.
        if (!old || old.hostId !== incoming.hostId || old.workspaceId !== incoming.workspaceId ||
            old.instanceId !== incoming.instanceId || old.revision >= incoming.revision) return previous;
        return { binding: bindingKey, snapshot: incoming, failed: false };
      });
    }) : () => {};
    let remoteBinding = "";
    const stopRemote = onRemoteTabUpdated(meta => {
      if (meta.id !== tabId) return;
      const key = JSON.stringify([meta.workspaceRoot, meta.sessionId, meta.sessionGeneration, meta.ready, meta.remote]);
      if (key === remoteBinding) return;
      remoteBinding = key;
      ++request.current;
      setResult(null);
      void reload();
    });
    const stopService = host.kind !== "none" ? host.native.onServiceState((state) => {
      ++request.current;
      if (state.phase === "ready") {
        setResult(null);
        void reload();
      } else {
        setResult({ binding: bindingKey, snapshot: null, failed: true });
      }
    }) : () => {};
    const focus = () => { void reload(); };
    window.addEventListener("focus", focus);
    return () => { ++request.current; stop(); stopSnapshots(); stopRemote(); stopService(); window.removeEventListener("focus", focus); };
  }, [reload]);

  const current = result?.binding === bindingKey ? result : null;
  const snapshot = current?.snapshot ?? null;
  const items = snapshot?.items ?? [];
  const warnings = snapshot ? items.filter(item => item.severity !== "info" && !dismissed.has(diagnosticDismissKey(snapshot, item))) : [];
  const dismiss = useCallback((id: string) => {
    if (snapshot) for (const item of snapshot.items) if (item.id === id) dismissed.add(diagnosticDismissKey(snapshot, item));
    changed(n => n + 1);
  }, [snapshot]);
  const openSource = useCallback(async (id: string) => {
    await app.OpenConfigDiagnosticSource?.(tabId, id);
  }, [tabId]);
  return { snapshot, items, warnings, loading: Boolean(tabId) && current === null, unavailable: current?.failed === true,
    reload, dismiss, openSource };
}
