import { Archive, ChevronDown, ChevronRight, LoaderCircle, MessageSquare, Plus, RotateCcw, Search } from "lucide-react";
import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { WorkspaceSessionSummary, WorkspaceSnapshot } from "../generated/desktopContract.generated";
import { app, onProjectTreeChanged } from "../lib/bridge";
import { useI18n } from "../lib/i18n";
import { useToast } from "../lib/toast";
import "./WorkspaceSessionBrowser.css";
import { topicActivityDateLabel, topicActivityLabel } from "../lib/projectTreeTopic";
import { retainPreparedSessionLabels, sessionIsBlank, sessionMetadataPending } from "../lib/workspaceSessionPresentation";

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
  const ordinary = visible.filter((row) => !sessionIsBlank(row)).slice(0, 5);
  const provisional = visible.find((row) => sessionIsBlank(row) && row.running);
  return provisional ? [...ordinary, provisional] : ordinary;
}

export function WorkspaceSessionBrowser({ active = true, archived = false, onOpenSession, onCreateSession }: {
  active?: boolean;
  archived?: boolean;
  onOpenSession: (ref: WorkspaceSessionSummary["ref"]) => Promise<void>;
  onCreateSession?: (workspace: WorkspaceSnapshot["workspaces"][number]) => Promise<void>;
}) {
  const { showToast } = useToast();
  const { t } = useI18n();
  const [snapshot, setSnapshot] = useState<WorkspaceSnapshot | null>(null);
  const [rows, setRows] = useState<SessionRows>({});
  const [query, setQuery] = useState("");
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [showAll, setShowAll] = useState<Set<string>>(new Set());
  const [activeSessionId, setActiveSessionId] = useState("");
  const [loading, setLoading] = useState(true);
  const [refreshFailed, setRefreshFailed] = useState(false);
  const openSequence = useRef(0);
  const reloadSequence = useRef(0);
  const draggedWorkspace = useRef("");
  const movingWorkspace = useRef(false);
  const [dropTarget, setDropTarget] = useState<{ id: string; after: boolean } | null>(null);
  const clearDrag = () => { draggedWorkspace.current = ""; setDropTarget(null); };

  const reload = useCallback(async () => {
    if (typeof app.GetWorkspaceSnapshot !== "function") return;
    const sequence = ++reloadSequence.current;
    setLoading(true);
    try {
      const [next, tabs] = await Promise.all([app.GetWorkspaceSnapshot(), app.ListTabs()]);
      const loaded = await Promise.all(next.workspaces.filter((workspace) => archived || workspace.visible).map(async (workspace) => {
        const page = await app.ListWorkspaceSessions(workspace.id, query, "", 200, archived);
        return [workspace.id, page.sessions] as const;
      }));
      if (sequence !== reloadSequence.current) return;
      setSnapshot(next);
      setRows(previous => Object.fromEntries(loaded.map(([id, sessions]) => [id, retainPreparedSessionLabels(sessions, previous[id] ?? [])])));
      setRefreshFailed(false);
      const active = tabs.find((tab) => tab.active && !tab.remote);
      setActiveSessionId(active?.session?.sessionId || active?.sessionId || "");
      setExpanded((current) => current.size > 0 ? current : new Set(next.workspaces.filter((workspace) => workspace.visible).map((workspace) => workspace.id)));
    } catch (error) {
      if (sequence === reloadSequence.current) {
        setRefreshFailed(true);
        showToast(error instanceof Error ? error.message : String(error), "error");
      }
    } finally {
      if (sequence === reloadSequence.current) setLoading(false);
    }
  }, [archived, query, showToast]);

  useEffect(() => {
    if (!active) return;
    void reload();
    const unsubscribe = onProjectTreeChanged(() => void reload());
    return () => { unsubscribe(); ++reloadSequence.current; };
  }, [active, reload]);

  useEffect(() => {
    if (!active || loading || refreshFailed || !Object.values(rows).some(group => group.some(sessionMetadataPending))) return;
    const timer = setTimeout(() => void reload(), 1000);
    return () => clearTimeout(timer);
  }, [active, loading, refreshFailed, rows, reload]);

  const label = (session: WorkspaceSessionSummary) => session.title || session.preview ||
    t(sessionMetadataPending(session) ? "workspaceBrowser.loading" : session.metadataStatus === "failed" ? "history.failedLoadHistory" : "workspaceBrowser.newSession");

  const visibleWorkspaces = useMemo(() => snapshot?.workspaces.filter((workspace) => archived || workspace.visible) ?? [], [snapshot, archived]);
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
      await onOpenSession(session.ref);
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
      {loading && !snapshot && <div className="workspace-browser__empty"><LoaderCircle className="spin" size={14} /> {t("workspaceBrowser.loading")}</div>}
      {refreshFailed &&
        <button className="workspace-browser__show-more" disabled={loading} onClick={() => void reload()}>{t("common.retry")}</button>}
      {visibleWorkspaces.map((workspace) => {
        const open = expanded.has(workspace.id);
        const workspaceRows = rows[workspace.id] ?? [];
        const allVisible = workspaceRows.filter((row) => archived ? row.archived : !row.archived);
        const canShowMore = !archived && !query && allVisible.filter((row) => !sessionIsBlank(row)).length > 5;
        const showingAll = showAll.has(workspace.id);
        const shown = workspaceSessionsForDisplay(workspaceRows, Boolean(query) || showingAll, archived);
        return (
          <section className="workspace-browser__workspace" key={workspace.id} data-workspace-id={workspace.id}>
            <div className="workspace-browser__workspace-heading"
              data-drop={dropTarget?.id === workspace.id ? dropTarget.after ? "after" : "before" : undefined}
              onDragOver={(event) => {
                if (!draggedWorkspace.current || draggedWorkspace.current === workspace.id || archived || query.trim()) return;
                event.preventDefault();
                event.dataTransfer.dropEffect = "move";
                const rect = event.currentTarget.getBoundingClientRect();
                setDropTarget({ id: workspace.id, after: event.clientY >= rect.top + rect.height / 2 });
              }}
              onDragLeave={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDropTarget(null); }}
              onDrop={(event) => {
                const source = draggedWorkspace.current;
                const rect = event.currentTarget.getBoundingClientRect();
                const after = event.clientY >= rect.top + rect.height / 2;
                clearDrag();
                if (!source || source === workspace.id || movingWorkspace.current || archived || query.trim()) return;
                event.preventDefault(); event.stopPropagation();
                const ordered = snapshot?.workspaces.filter((item) => item.id !== source) ?? [];
                const before = after ? ordered[ordered.findIndex((item) => item.id === workspace.id) + 1]?.id ?? "" : workspace.id;
                movingWorkspace.current = true;
                void mutate(() => app.MoveWorkspace(source, before)).finally(() => { movingWorkspace.current = false; });
              }}>
              <button className="workspace-browser__workspace-title" type="button" title={workspace.title || t("workspaceBrowser.workspace")} aria-expanded={open}
                draggable={!archived && !query.trim()}
                onDragStart={(event) => {
                  if (movingWorkspace.current) { event.preventDefault(); return; }
                  draggedWorkspace.current = workspace.id;
                  event.dataTransfer.effectAllowed = "move";
                  event.dataTransfer.setData("application/x-reasonix-workspace", workspace.id);
                }} onDragEnd={clearDrag} onClick={() => setExpanded((current) => {
                const next = new Set(current);
                if (next.has(workspace.id)) next.delete(workspace.id); else next.add(workspace.id);
                return next;
              })}>
                {open ? <ChevronDown size={13} /> : <ChevronRight size={13} />}<span className="workspace-browser__workspace-label">{workspace.title || t("workspaceBrowser.workspace")}</span>
                <Suspense fallback={null}><RuntimeActivity target={{
                  scope: workspace.id === GLOBAL_WORKSPACE_ID ? "global" : "project", root: workspace.root,
                }} /></Suspense>
              </button>
              {!archived && onCreateSession && <button className="workspace-browser__workspace-create" type="button" aria-label={t("workspaceBrowser.create")}
                onClick={() => void mutate(() => onCreateSession(workspace))}>
                <Plus size={13} />
              </button>}
            </div>
            {open && shown.map((session) => (
              <div className={`workspace-browser__session${activeSessionId === session.ref.sessionId ? " workspace-browser__session--active" : ""}`} key={session.ref.sessionId} data-health={session.health} data-session-id={session.ref.sessionId}>
                <button className="workspace-browser__session-open" type="button" data-session-id={session.ref.sessionId}
                  title={[label(session), topicActivityDateLabel(session.updatedAt || session.createdAt)].filter(Boolean).join("\n")}
                  aria-current={activeSessionId === session.ref.sessionId ? "page" : undefined} onClick={() => void openSession(session)}>
                  <MessageSquare size={13} aria-hidden="true" />
                  <strong className="workspace-browser__session-label">{label(session)}</strong>
                  <span className="workspace-browser__session-time" aria-hidden="true">{topicActivityLabel(session.updatedAt || session.createdAt, t, true)}</span>
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
