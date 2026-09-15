import { MessageSquare, RotateCcw, Search } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import type { WorkspaceSessionSummary } from "../generated/desktopContract.generated";
import { app, onProjectTreeChanged } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { useToast } from "../lib/toast";
import { retainPreparedSessionLabels, sessionMetadataPending } from "../lib/workspaceSessionPresentation";
import "./ArchivedSessionsList.css";

type ArchiveGroup = { id: string; title: string; sessions: WorkspaceSessionSummary[] };

// Recovery is a management-page view; the sidebar has only ProjectTree.
export function ArchivedSessionsList({ active, onOpenSession }: {
  active: boolean;
  onOpenSession: (ref: WorkspaceSessionSummary["ref"]) => Promise<void>;
}) {
  const t = useT();
  const { showToast } = useToast();
  const [groups, setGroups] = useState<ArchiveGroup[]>([]);
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [busy, setBusy] = useState(false);
  const generation = useRef(0);
  const mutating = useRef(false);
  const reload = useCallback(async () => {
    const sequence = ++generation.current;
    setLoading(true);
    try {
      const snapshot = await app.GetWorkspaceSnapshot();
      if (sequence !== generation.current) return;
      const next: ArchiveGroup[] = [];
      for (const workspace of snapshot.workspaces) {
        const sessions: WorkspaceSessionSummary[] = [];
        let cursor = "";
        do {
          const page = await app.ListWorkspaceSessions(workspace.id, query, cursor, 200, true);
          if (sequence !== generation.current) return;
          sessions.push(...page.sessions.filter(row => row.archived));
          cursor = page.nextCursor ?? "";
        } while (cursor);
        if (sessions.length) next.push({ id: workspace.id, title: workspace.title, sessions });
      }
      setGroups(previous => next.map(group => ({ ...group, sessions: retainPreparedSessionLabels(group.sessions, previous.find(item => item.id === group.id)?.sessions ?? []) })));
      setFailed(false);
    } catch (error) {
      if (sequence !== generation.current) return;
      setFailed(true);
      showToast(error instanceof Error ? error.message : String(error), "error");
    } finally {
      if (sequence === generation.current) setLoading(false);
    }
  }, [query, showToast]);
  useEffect(() => {
    if (!active) return;
    void reload();
    const unsubscribe = onProjectTreeChanged(() => void reload());
    return () => { ++generation.current; unsubscribe(); };
  }, [active, reload]);
  useEffect(() => {
    if (!active || loading || failed || !groups.some(group => group.sessions.some(sessionMetadataPending))) return;
    const timer = setTimeout(() => void reload(), 1000);
    return () => clearTimeout(timer);
  }, [active, loading, failed, groups, reload]);
  const open = async (row: WorkspaceSessionSummary) => {
    try { await onOpenSession(row.ref); }
    catch (error) { showToast(error instanceof Error ? error.message : String(error), "error"); }
  };
  const restore = async (row: WorkspaceSessionSummary) => {
    if (mutating.current) return;
    mutating.current = true;
    setBusy(true);
    ++generation.current;
    try {
      await app.RestoreCanonicalSession(row.ref);
      setGroups(current => current.map(group => ({ ...group, sessions: group.sessions.filter(item => item.ref.sessionId !== row.ref.sessionId) })).filter(group => group.sessions.length));
      await reload();
    } catch (error) { showToast(error instanceof Error ? error.message : String(error), "error"); }
    finally { mutating.current = false; setBusy(false); }
  };
  return <div className="archived-sessions">
    <label className="archived-sessions__search"><Search size={14} aria-hidden="true" />
      <input aria-label={t("history.searchPlaceholder")} placeholder={t("history.searchPlaceholder")} value={query} onChange={event => setQuery(event.target.value)} />
    </label>
    {loading && <div role="status">{t("common.loading")}</div>}
    {failed && <div role="alert">{t("history.failedLoadHistory")} <button className="btn btn--small" disabled={loading} onClick={() => void reload()}>{t("common.retry")}</button></div>}
    {!loading && !failed && !groups.length && <div className="archived-sessions__empty">{t("history.noArchivedSessions")}</div>}
    {groups.map(group => <section key={group.id}>
      <h3 className="archived-sessions__title" title={group.title}>{group.title}</h3>
      {group.sessions.map(row => {
        const label = row.title || row.preview || t(sessionMetadataPending(row) ? "common.loading" : row.metadataStatus === "failed" ? "history.failedLoadHistory" : "history.emptySession");
        return <div className="archived-sessions__row" key={row.ref.sessionId}>
          <button className="archived-sessions__open" title={label} disabled={busy} onClick={() => void open(row)}><MessageSquare size={14} aria-hidden="true" /><span>{label}</span></button>
          <button className="btn btn--small" aria-label={t("history.restoreSession")} disabled={busy} onClick={() => void restore(row)}><RotateCcw size={14} aria-hidden="true" />{t("history.restore")}</button>
        </div>;
      })}
    </section>)}
  </div>;
}
