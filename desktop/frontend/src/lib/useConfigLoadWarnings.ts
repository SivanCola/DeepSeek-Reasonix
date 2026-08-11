import { useCallback, useEffect, useRef, useState } from "react";

export function normalizeConfigLoadWarnings(payload: unknown): string[] {
  if (!Array.isArray(payload)) return [];
  const seen = new Set<string>();
  const warnings: string[] = [];
  for (const value of payload) {
    if (typeof value !== "string") continue;
    const warning = value.trim();
    if (!warning || seen.has(warning)) continue;
    seen.add(warning);
    warnings.push(warning);
  }
  return warnings;
}

export function configLoadWarningsKey(warnings: readonly string[]): string {
  return warnings.length > 0 ? JSON.stringify(warnings) : "";
}

export function subscribeConfigLoadWarnings(cb: (warnings: string[]) => void): () => void {
  if (typeof window !== "undefined" && window.go?.main?.App && window.runtime) {
    return window.runtime.EventsOn("config:load-warnings", (payload?: unknown) => {
      const warnings = normalizeConfigLoadWarnings(payload);
      if (warnings.length > 0) cb(warnings);
    });
  }
  return () => {};
}

export function useConfigLoadWarnings() {
  const [configLoadWarnings, setConfigLoadWarnings] = useState<string[]>([]);
  const eventRevision = useRef(0);
  const seenKeys = useRef(new Set<string>());
  const present = useCallback((payload: unknown, resetSeen = false) => {
    const warnings = normalizeConfigLoadWarnings(payload);
    if (resetSeen) seenKeys.current.clear();
    const key = configLoadWarningsKey(warnings);
    if (!key) {
      seenKeys.current.clear();
      setConfigLoadWarnings([]);
      return;
    }
    if (seenKeys.current.has(key)) return;
    seenKeys.current.add(key);
    setConfigLoadWarnings(warnings);
  }, []);

  useEffect(() => subscribeConfigLoadWarnings((warnings) => {
    eventRevision.current += 1;
    present(warnings);
  }), [present]);

  const beginSnapshot = useCallback(() => eventRevision.current, []);
  const applySnapshot = useCallback((payload: unknown, revision: number) => {
    if (revision === eventRevision.current) present(payload);
  }, [present]);
  const reload = useCallback((payload: unknown) => present(payload, true), [present]);
  const dismiss = useCallback(() => setConfigLoadWarnings([]), []);

  return { configLoadWarnings, beginSnapshot, applySnapshot, reload, dismiss };
}
