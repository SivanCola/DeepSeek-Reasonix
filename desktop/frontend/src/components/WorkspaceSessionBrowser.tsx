import { Archive, ChevronDown, ChevronRight, History, LoaderCircle, MessageSquare, Plus, RotateCcw, Search } from "lucide-react";
import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { WorkspaceSessionSummary, WorkspaceSnapshot } from "../generated/desktopContract.generated";
import { app, onProjectTreeChanged } from "../lib/bridge";
import { useI18n } from "../lib/i18n";
import { useToast } from "../lib/toast";

// Workspace activity comes from the runtime store, not from the session rows:
// a background job keeps a workspace active after its turn goes idle, and the
// row projection does not carry that state.
const RuntimeActivity = lazy(() => import("./RuntimeActivityIndicator"));

// Mirrors workspacestate.GlobalWorkspaceID; the global workspace has no root.
const GLOBAL_WORKSPACE_ID = "global";

type SessionRows = Record<string, WorkspaceSessionSummary[]>;

export function workspaceSessionsForDisplay(rows: WorkspaceSessionSummary[], searching: boolean, archived: boolean): WorkspaceSessionSummary[] {
  if (archived) return rows.filter((row) => row.archived);
  const visible = rows.filter((row) => !row.archived);
  if (searching) return visible;
  const ordinary = visible.filter((row) => !row.blank).slice(0, 5);
  const provisional = visible.find((row) => row.blank && row.running);
  return provisional ? [...ordinary, provisional] : ordinary;
}

export function WorkspaceSessionBrowser() {
  const { showToast } = useToast();
  const { t } = useI18n();
  const [snapshot, setSnapshot] = useState<WorkspaceSnapshot | null>(null);
  const [rows, setRows] = useState<SessionRows>({});
  const [query, setQuery] = useState("");
  const [archived, setArchived] = useState(false);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [showAll, setShowAll] = useState<Set<string>>(new Set());
  const [activeSessionId, setActiveSessionId] = useState("");
  const [loading, setLoading] = useState(true);
  const openSequence = useRef(0);

  const reload = useCallback(async () => {
    if (typeof app.GetWorkspaceSnapshot !== "function") return;
    setLoading(true);
    try {
      const [next, tabs] = await Promise.all([app.GetWorkspaceSnapshot(), app.ListTabs()]);
      const loaded = await Promise.all(next.workspaces.filter((workspace) => workspace.visible).map(async (workspace) => {
        const page = await app.ListWorkspaceSessions(workspace.id, query, "", 200, archived);
        return [workspace.id, page.sessions] as const;
      }));
      setSnapshot(next);
      setRows(Object.fromEntries(loaded));
      const active = tabs.find((tab) => tab.active && !tab.remote);
      setActiveSessionId(active?.session?.sessionId || active?.sessionId || "");
      setExpanded((current) => current.size > 0 ? current : new Set(next.workspaces.filter((workspace) => workspace.visible).map((workspace) => workspace.id)));
    } catch (error) {
      showToast(error instanceof Error ? error.message : String(error), "error");
    } finally {
      setLoading(false);
    }
  }, [archived, query, showToast]);

  useEffect(() => {
    void reload();
    return onProjectTreeChanged(() => void reload());
  }, [reload]);

  const visibleWorkspaces = useMemo(() => snapshot?.workspaces.filter((workspace) => workspace.visible) ?? [], [snapshot]);
  const mutate = async (task: () => Promise<void>) => {
    try {
      await task();
      await reload();
    } catch (error) {
      showToast(error instanceof Error ? error.message : String(error), "error");
    }
  };
  const openSession = async (session: WorkspaceSessionSummary) => {
    const sequence = ++openSequence.current;
    try {
      await app.OpenSession(session.ref);
      if (sequence === openSequence.current) await reload();
    } catch (error) {
      if (sequence === openSequence.current) showToast(error instanceof Error ? error.message : String(error), "error");
    }
  };

  return (
    <div className="workspace-browser" data-testid="workspace-session-browser">
      <div className="workspace-browser__search">
        <Search size={13} aria-hidden="true" />
        <input aria-label={t("workspaceBrowser.search")} value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("workspaceBrowser.search")} />
      </div>
      <button className="workspace-browser__archive-toggle" type="button" onClick={() => setArchived((value) => !value)}>
        <History size={13} aria-hidden="true" />{archived ? t("workspaceBrowser.back") : t("workspaceBrowser.archived")}
      </button>
      {loading && !snapshot && <div className="workspace-browser__empty"><LoaderCircle className="spin" size={14} /> {t("workspaceBrowser.loading")}</div>}
      {visibleWorkspaces.map((workspace) => {
        const open = expanded.has(workspace.id);
        const workspaceRows = rows[workspace.id] ?? [];
        const allVisible = workspaceRows.filter((row) => archived ? row.archived : !row.archived);
        const canShowMore = !archived && !query && allVisible.filter((row) => !row.blank).length > 5;
        const showingAll = showAll.has(workspace.id);
        const shown = workspaceSessionsForDisplay(workspaceRows, Boolean(query) || showingAll, archived);
        return (
          <section className="workspace-browser__workspace" key={workspace.id}>
            <div className="workspace-browser__workspace-heading">
              <button className="workspace-browser__workspace-title" type="button" onClick={() => setExpanded((current) => {
                const next = new Set(current);
                if (next.has(workspace.id)) next.delete(workspace.id); else next.add(workspace.id);
                return next;
              })}>
                {open ? <ChevronDown size={13} /> : <ChevronRight size={13} />}<span>{workspace.title || t("workspaceBrowser.workspace")}</span>
                <Suspense fallback={null}><RuntimeActivity target={{
                  scope: workspace.id === GLOBAL_WORKSPACE_ID ? "global" : "project", root: workspace.root,
                }} /></Suspense>
              </button>
              <button className="workspace-browser__workspace-create" type="button" aria-label={t("workspaceBrowser.create")}
                onClick={() => void mutate(async () => { await app.CreateSession(workspace.id); })}>
                <Plus size={13} />
              </button>
            </div>
            {open && shown.map((session) => (
              <div className={`workspace-browser__session${activeSessionId === session.ref.sessionId ? " workspace-browser__session--active" : ""}`} key={session.ref.sessionId} data-health={session.health} data-session-id={session.ref.sessionId}>
                <button className="workspace-browser__session-open" type="button" data-session-id={session.ref.sessionId}
                  aria-current={activeSessionId === session.ref.sessionId ? "page" : undefined} onClick={() => void openSession(session)}>
                  <MessageSquare size={13} aria-hidden="true" />
                  <span><strong>{session.title || session.preview || t("workspaceBrowser.newSession")}</strong><small>{session.preview || (session.metadataStatus === "ready" ? t("workspaceBrowser.noMessages") : t("workspaceBrowser.indexing"))}</small></span>
                  {session.running && <i aria-label={t("workspaceBrowser.running")} />}
                </button>
                <button className="workspace-browser__session-action" type="button" aria-label={archived ? t("workspaceBrowser.restore") : t("workspaceBrowser.archive")}
                  onClick={() => void mutate(() => archived ? app.RestoreCanonicalSession(session.ref) : app.ArchiveCanonicalSession(session.ref))}>
                  {archived ? <RotateCcw size={13} /> : <Archive size={13} />}
                </button>
              </div>
            ))}
            {open && canShowMore && (
              <button className="workspace-browser__show-more" type="button" onClick={() => setShowAll((current) => {
                const next = new Set(current);
                if (next.has(workspace.id)) next.delete(workspace.id); else next.add(workspace.id);
                return next;
              })}>{showingAll ? t("common.collapse") : t("projectTree.loadMore")}</button>
            )}
            {open && shown.length === 0 && <div className="workspace-browser__empty">{archived ? t("workspaceBrowser.noArchived") : t("workspaceBrowser.empty")}</div>}
          </section>
        );
      })}
    </div>
  );
}
