import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { CSSProperties, KeyboardEvent, PointerEvent as ReactPointerEvent } from "react";
import {
  SquarePen,
  Brain,
  Blocks,
  CircleGauge,
  FileText,
  GitBranch,
  History,
  Settings as SettingsIcon,
  Pencil,
  Pin,
  MoreHorizontal,
  PanelLeftClose,
  PanelLeftOpen,
  PanelRightClose,
  PanelRightOpen,
  RefreshCw,
  Trash2,
} from "lucide-react";
import logo from "./assets/logo.svg";
import { asArray } from "./lib/array";
import { useT } from "./lib/i18n";
import { useController } from "./lib/useController";
import { app } from "./lib/bridge";
import { Transcript } from "./components/Transcript";
import { Composer } from "./components/Composer";
import { TodoPanel } from "./components/TodoPanel";
import { ApprovalModal } from "./components/ApprovalModal";
import { AskCard } from "./components/AskCard";
import { StatusBar } from "./components/StatusBar";
import { MemoryPanel } from "./components/MemoryPanel";
import { HistoryPanel } from "./components/HistoryPanel";
import { SettingsPanel } from "./components/SettingsPanel";
import { CapabilitiesPanel } from "./components/CapabilitiesPanel";
import { UpdateBanner } from "./components/UpdateBanner";
import { ContextPanel } from "./components/ContextPanel";
import { WorkspacePanel } from "./components/WorkspacePanel";
import { Tooltip } from "./components/Tooltip";
import { OnboardingOverlay } from "./components/OnboardingOverlay";
import { TabBar } from "./components/TabBar";
import { ProjectTree } from "./components/ProjectTree";
import { parseTodos } from "./lib/tools";
import type { ComposerInsertRequest, MemoryView, Meta, Mode, SessionMeta, TabMeta } from "./lib/types";
import { loadLayoutSize, saveLayoutSize } from "./lib/layoutPreferences";
import { applyTheme, getTheme, getThemeStyle, isThemeStyle, themeForStyle, type Theme } from "./lib/theme";

const SIDEBAR_COLLAPSED_KEY = "reasonix.sidebar.collapsed";
const SIDEBAR_COLLAPSED_WIDTH = 68;
const SIDEBAR_DEFAULT_WIDTH = 264;
const SIDEBAR_MIN_WIDTH = 228;
const SIDEBAR_MAX_WIDTH = 420;
const CHAT_MIN_WIDTH = 420;

function isThemeMode(value: string): value is Theme {
  return value === "auto" || value === "light" || value === "dark";
}
const CONTEXT_PANEL_MIN_WIDTH = 340;
const CONTEXT_PANEL_MAX_WIDTH = 420;
const WORKSPACE_PANEL_MIN_WIDTH = CONTEXT_PANEL_MIN_WIDTH;
const WORKSPACE_PANEL_DEFAULT_WIDTH = 380;
const WORKSPACE_PANEL_FILES_DEFAULT_WIDTH = 760;
const WORKSPACE_PANEL_MAX_WIDTH = 920;
const WORKSPACE_PANEL_MAX_RATIO = 0.58;

type RightDockMode = "context" | "files" | "changed";
const SHOW_CONTEXT_DOCK = false;

function clampSidebarWidth(width: number): number {
  return Math.min(SIDEBAR_MAX_WIDTH, Math.max(SIDEBAR_MIN_WIDTH, Math.round(width)));
}

function clampWorkspacePanelWidth(width: number, sidebarWidth = SIDEBAR_DEFAULT_WIDTH, viewportWidth = 1440): number {
  const maxByRatio = Math.floor(viewportWidth * WORKSPACE_PANEL_MAX_RATIO);
  const maxByChat = Math.floor(viewportWidth - sidebarWidth - CHAT_MIN_WIDTH);
  const max = Math.max(WORKSPACE_PANEL_MIN_WIDTH, Math.min(WORKSPACE_PANEL_MAX_WIDTH, maxByRatio, maxByChat));
  return Math.min(max, Math.max(WORKSPACE_PANEL_MIN_WIDTH, Math.round(width)));
}

function loadSidebarCollapsed(): boolean {
  if (typeof window === "undefined") return false;
  try {
    return window.localStorage.getItem(SIDEBAR_COLLAPSED_KEY) === "1";
  } catch {
    return false;
  }
}

function saveSidebarCollapsed(collapsed: boolean): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(SIDEBAR_COLLAPSED_KEY, collapsed ? "1" : "0");
  } catch {
    /* ignore storage failures */
  }
}

function loadSidebarWidth(): number {
  return loadLayoutSize("sidebarWidth", SIDEBAR_DEFAULT_WIDTH, clampSidebarWidth);
}

function saveSidebarWidth(width: number): void {
  saveLayoutSize("sidebarWidth", width, clampSidebarWidth);
}

function loadWorkspacePanelWidth(): number {
  return loadLayoutSize("workspacePanelWidth", WORKSPACE_PANEL_DEFAULT_WIDTH, clampWorkspacePanelWidth);
}

function saveWorkspacePanelWidth(width: number): void {
  saveLayoutSize("workspacePanelWidth", width);
}

function topicTitle(tab?: TabMeta): string {
  if (!tab) return "Global";
  if (tab.scope === "global") return tab.topicTitle || "Global";
  return `${tab.workspaceName || "Project"} / ${tab.topicTitle || "Untitled"}`;
}

function topicScopeLabel(tab?: TabMeta): string {
  if (!tab || tab.scope === "global") return "范围：全局";
  return `项目 · ${tab.workspaceName || tab.workspaceRoot || "Project"}`;
}

function appChromeScopeLabel(tab?: TabMeta, meta?: Meta): string {
  if (tab?.scope === "project") return tab.workspaceName || tab.workspaceRoot || "Project";
  if (tab?.scope === "global") return tab.topicTitle || "Global";
  return workspaceDisplayName(meta?.cwd) || meta?.label || "Global";
}

function workspaceDisplayName(path?: string): string {
  if (!path) return "";
  const parts = path.split(/[/\\]/).filter(Boolean);
  return parts.length > 0 ? parts[parts.length - 1] : path;
}

function formatContextWindow(tokens: number): string {
  if (!tokens) return "context";
  if (tokens >= 1_000_000) return `${Math.round(tokens / 1_000_000)}M context`;
  if (tokens >= 1000) return `${Math.round(tokens / 1000)}K context`;
  return `${tokens} context`;
}

export default function App() {
  const {
    state,
    activeTabId,
    send,
    notice,
    cancel,
    approve,
    answerQuestion,
    setControllerMode,
    newSession,
    listSessions,
    listTrashedSessions,
    resumeSession,
    previewSession,
    deleteSession,
    restoreSession,
    purgeTrashedSession,
    renameSession,
    refreshMeta,
    pickWorkspace,
    switchWorkspace,
    rewind,
    setModel,
    setEffort,
    fetchMemory,
    remember,
    forget,
    saveDoc,
    switchTab,
    openProjectTab,
    openGlobalTab,
    closeTab,
  } = useController();
  const t = useT();
  const [mode, setMode] = useState<Mode>("normal");
  const [tabMetas, setTabMetas] = useState<TabMeta[]>([]);
  // null until the mount probe resolves; true shows the overlay. Probed once —
  // clearing the key mid-session is the Settings panel's job, not the gate's.
  const [needsOnboarding, setNeedsOnboarding] = useState<boolean | null>(null);
  const [memView, setMemView] = useState<MemoryView | null>(null);
  const [histView, setHistView] = useState<{ kind: "history" | "trash"; sessions: SessionMeta[] } | null>(null);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(loadSidebarCollapsed);
  const [sidebarWidth, setSidebarWidth] = useState(loadSidebarWidth);
  const [sidebarResizing, setSidebarResizing] = useState(false);
  const [workspacePanelOpen, setWorkspacePanelOpen] = useState(true);
  const [workspacePanelWidth, setWorkspacePanelWidth] = useState(loadWorkspacePanelWidth);
  const [workspacePanelResizing, setWorkspacePanelResizing] = useState(false);
  const [workspacePanelMaximized, setWorkspacePanelMaximized] = useState(false);
  const [rightDockMode, setRightDockMode] = useState<RightDockMode>("files");
  const [dockRefreshKey, setDockRefreshKey] = useState(0);
  const [composerInsertRequest, setComposerInsertRequest] = useState<ComposerInsertRequest | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [capsOpen, setCapsOpen] = useState(false);
  const [pendingPlanRevision, setPendingPlanRevision] = useState<string | null>(null);
  const [viewportWidth, setViewportWidth] = useState(() => (typeof window === "undefined" ? 1440 : window.innerWidth));
  const [footerHeight, setFooterHeight] = useState(0);
  const footerRef = useRef<HTMLElement>(null);
  const effectiveSidebarWidth = sidebarCollapsed ? SIDEBAR_COLLAPSED_WIDTH : sidebarWidth;
  const effectiveWorkspacePanelWidth = useMemo(
    () => {
      const width = clampWorkspacePanelWidth(workspacePanelWidth, effectiveSidebarWidth, viewportWidth);
      if (rightDockMode === "context") return Math.min(CONTEXT_PANEL_MAX_WIDTH, width);
      return width;
    },
    [effectiveSidebarWidth, rightDockMode, viewportWidth, workspacePanelWidth],
  );
  const activeTab = useMemo(
    () => tabMetas.find((tab) => tab.id === activeTabId) ?? tabMetas.find((tab) => tab.active),
    [activeTabId, tabMetas],
  );

  const syncModeToController = useCallback((m: Mode) => setControllerMode(m), [setControllerMode]);

  // applyMode is the single source of truth for the input mode: it updates the
  // local pill and pushes the matching gate state to the controller (plan = read
  // only; yolo = auto-approve every tool call). normal clears both.
  const applyMode = useCallback(
    (m: Mode) => {
      setMode(m);
      void syncModeToController(m);
    },
    [syncModeToController],
  );
  // Shift+Tab cycles auto(normal) → plan → yolo → auto.
  const cycleMode = useCallback(() => {
    applyMode(mode === "normal" ? "plan" : mode === "plan" ? "yolo" : "normal");
  }, [mode, applyMode]);

  // Switching models rebuilds the controller, which starts in normal mode — so
  // re-apply the current mode, or the pill would say plan/YOLO while the fresh
  // controller silently uses normal gating.
  const switchModel = useCallback(
    async (name: string) => {
      await setModel(name);
      await syncModeToController(mode);
    },
    [setModel, mode, syncModeToController],
  );

  // Startup and workspace/model rebuilds create a fresh controller in normal
  // mode. Re-apply the UI mode once the controller is ready, including the case
  // where the user picked YOLO while boot was still loading and SetBypass was a
  // harmless no-op.
  useEffect(() => {
    if (state.meta?.ready !== true || mode === "normal") return;
    void syncModeToController(mode);
  }, [state.meta, mode, syncModeToController]);

  // The live task list pinned above the composer comes from the most recent
  // successful top-level todo_write result; failed or still-running attempts do
  // not advance the canonical panel state. It stays visible while work remains,
  // clears itself once every item is completed, and can be dismissed by the user
  // (the ✕). A dismissal is keyed to that list's id, so a fresh accepted
  // todo_write brings the panel back.
  const todoItem = useMemo(() => {
    for (let i = state.items.length - 1; i >= 0; i--) {
      const it = state.items[i];
      if (it.kind === "tool" && it.name === "todo_write" && !it.parentId && it.status === "done" && !it.error) return it;
    }
    return null;
  }, [state.items]);
  const todos = useMemo(() => (todoItem ? parseTodos(todoItem.args) : []), [todoItem]);
  const [dismissedTodo, setDismissedTodo] = useState<string | null>(null);
  const showTodos =
    !!todoItem &&
    todoItem.id !== dismissedTodo &&
    todos.length > 0 &&
    todos.some((t) => t.status !== "completed");

  useEffect(() => {
    if (!pendingPlanRevision || state.running) return;
    const text = pendingPlanRevision;
    setPendingPlanRevision(null);
    send(text);
  }, [pendingPlanRevision, send, state.running]);

  // Memory drawer: opening fetches a fresh snapshot; writes re-fetch so the
  // panel reflects what landed on disk.
  const openMemory = useCallback(async () => {
    setMemView(await fetchMemory());
  }, [fetchMemory]);

  const closeMemory = useCallback(() => setMemView(null), []);

  // handleSend intercepts the slash commands that need a desktop-native action
  // before they reach the backend: "/model <ref>" rebuilds on that model, and
  // "/memory" opens the memory drawer. Everything else — skills (/init, …),
  // custom commands, bare /model and the other read-only management verbs
  // (/skill, /hooks, /mcp) — goes straight to Submit, which the controller
  // resolves (a turn, or a listing Notice).
  const handleSend = useCallback(
    async (displayText: string, submitText = displayText) => {
      const trimmed = displayText.trim();
      const model = /^\/model\s+(\S+)$/.exec(trimmed);
      if (model) {
        void switchModel(model[1]);
        return;
      }
      if (trimmed === "/memory") {
        void openMemory();
        return;
      }
      const theme = /^\/theme(?:\s+(\S+))?$/.exec(trimmed);
      if (theme) {
        const arg = theme[1]?.toLowerCase();
        if (!arg) {
          const cur = getTheme();
          notice(t("settings.themeCurrent", { theme: cur, style: getThemeStyle(cur) }));
          return;
        }
        if (isThemeMode(arg)) {
          const next = arg;
          const style = getThemeStyle(next);
          applyTheme(next, style);
          notice(t("settings.themeChanged", { theme: next, style }));
          return;
        }
        if (isThemeStyle(arg)) {
          const next = themeForStyle(arg);
          applyTheme(next, arg);
          notice(t("settings.themeChanged", { theme: next, style: arg }));
          return;
        }
        notice(t("settings.themeUnknown", { name: arg }), "warn");
        return;
      }
      await syncModeToController(mode);
      send(trimmed, submitText.trim());
    },
    [switchModel, openMemory, syncModeToController, mode, send, notice, t],
  );

  const refreshTabMetas = useCallback(async () => {
    setTabMetas(asArray(await app.ListTabs().catch(() => [] as TabMeta[])));
  }, []);

  useEffect(() => {
    void refreshTabMetas();
    const id = window.setInterval(() => void refreshTabMetas(), 2000);
    return () => window.clearInterval(id);
  }, [refreshTabMetas]);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const needs = await app.NeedsOnboarding();
        if (!cancelled) setNeedsOnboarding(needs);
      } catch {
        // Bridge unavailable (browser dev seam) — skip the gate; a real key
        // failure still surfaces via the topbar startupError banner.
        if (!cancelled) setNeedsOnboarding(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    const onResize = () => setViewportWidth(window.innerWidth);
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);

  useEffect(() => {
    const el = footerRef.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const update = () => setFooterHeight(Math.round(el.getBoundingClientRect().height));
    update();
    const observer = new ResizeObserver(update);
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  const startNewSession = useCallback(async () => {
    await newSession();
  }, [newSession]);

  const toggleSidebar = useCallback(() => {
    setSidebarCollapsed((collapsed) => {
      const next = !collapsed;
      saveSidebarCollapsed(next);
      return next;
    });
  }, []);

  const setExpandedSidebarWidth = useCallback((width: number) => {
    const next = clampSidebarWidth(width);
    setSidebarWidth(next);
    saveSidebarWidth(next);
  }, []);

  const startSidebarResize = useCallback(
    (event: ReactPointerEvent<HTMLButtonElement>) => {
      if (sidebarCollapsed) return;
      event.preventDefault();
      setSidebarResizing(true);
      let nextWidth = sidebarWidth;
      const onMove = (moveEvent: PointerEvent) => {
        nextWidth = clampSidebarWidth(moveEvent.clientX);
        setSidebarWidth(nextWidth);
      };
      const onDone = () => {
        setSidebarWidth(nextWidth);
        saveSidebarWidth(nextWidth);
        setSidebarResizing(false);
        window.removeEventListener("pointermove", onMove);
        window.removeEventListener("pointerup", onDone);
        window.removeEventListener("pointercancel", onDone);
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
      };
      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
      window.addEventListener("pointermove", onMove);
      window.addEventListener("pointerup", onDone);
      window.addEventListener("pointercancel", onDone);
    },
    [sidebarCollapsed, sidebarWidth],
  );

  const resizeSidebarWithKeyboard = useCallback(
    (event: KeyboardEvent<HTMLButtonElement>) => {
      if (sidebarCollapsed) return;
      if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
        event.preventDefault();
        setExpandedSidebarWidth(sidebarWidth + (event.key === "ArrowRight" ? 16 : -16));
      } else if (event.key === "Home") {
        event.preventDefault();
        setExpandedSidebarWidth(SIDEBAR_MIN_WIDTH);
      } else if (event.key === "End") {
        event.preventDefault();
        setExpandedSidebarWidth(SIDEBAR_MAX_WIDTH);
      }
    },
    [setExpandedSidebarWidth, sidebarCollapsed, sidebarWidth],
  );

  const setSavedWorkspacePanelWidth = useCallback(
    (width: number) => {
      const next = clampWorkspacePanelWidth(width, effectiveSidebarWidth, viewportWidth);
      setWorkspacePanelWidth(next);
      saveWorkspacePanelWidth(next);
    },
    [effectiveSidebarWidth, viewportWidth],
  );

  const startWorkspacePanelResize = useCallback(
    (event: ReactPointerEvent<HTMLButtonElement>) => {
      if (!workspacePanelOpen) return;
      event.preventDefault();
      setWorkspacePanelResizing(true);
      let nextWidth = effectiveWorkspacePanelWidth;
      const onMove = (moveEvent: PointerEvent) => {
        nextWidth = clampWorkspacePanelWidth(window.innerWidth - moveEvent.clientX, effectiveSidebarWidth, window.innerWidth);
        setWorkspacePanelWidth(nextWidth);
      };
      const onDone = () => {
        setWorkspacePanelWidth(nextWidth);
        saveWorkspacePanelWidth(nextWidth);
        setWorkspacePanelResizing(false);
        window.removeEventListener("pointermove", onMove);
        window.removeEventListener("pointerup", onDone);
        window.removeEventListener("pointercancel", onDone);
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
      };
      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
      window.addEventListener("pointermove", onMove);
      window.addEventListener("pointerup", onDone);
      window.addEventListener("pointercancel", onDone);
    },
    [effectiveSidebarWidth, effectiveWorkspacePanelWidth, workspacePanelOpen],
  );

  const resizeWorkspacePanelWithKeyboard = useCallback(
    (event: KeyboardEvent<HTMLButtonElement>) => {
      if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
        event.preventDefault();
        setSavedWorkspacePanelWidth(effectiveWorkspacePanelWidth + (event.key === "ArrowLeft" ? 16 : -16));
      } else if (event.key === "Home") {
        event.preventDefault();
        setSavedWorkspacePanelWidth(WORKSPACE_PANEL_MIN_WIDTH);
      } else if (event.key === "End") {
        event.preventDefault();
        setSavedWorkspacePanelWidth(WORKSPACE_PANEL_MAX_WIDTH);
      }
    },
    [effectiveWorkspacePanelWidth, setSavedWorkspacePanelWidth],
  );

  const openRightDockMode = useCallback(
    (mode: RightDockMode) => {
      setRightDockMode(mode);
      setWorkspacePanelOpen(true);
      if (mode !== "context") {
        setSavedWorkspacePanelWidth(Math.max(effectiveWorkspacePanelWidth, WORKSPACE_PANEL_FILES_DEFAULT_WIDTH));
      } else {
        setWorkspacePanelMaximized(false);
      }
    },
    [effectiveWorkspacePanelWidth, setSavedWorkspacePanelWidth],
  );

  const layoutStyle = useMemo(
    () =>
      ({
        "--sidebar-expanded-width": `${sidebarWidth}px`,
        "--workspace-width": `${effectiveWorkspacePanelWidth}px`,
      }) as CSSProperties,
    [effectiveWorkspacePanelWidth, sidebarWidth],
  );

  const setWorkspacePanel = useCallback((open: boolean) => {
    setWorkspacePanelOpen(open);
  }, []);

  const toggleWorkspacePanel = useCallback(() => {
    setWorkspacePanelOpen((open) => {
      const next = !open;
      return next;
    });
  }, []);

  const addWorkspaceTextToComposer = useCallback((text: string) => {
    setComposerInsertRequest({ id: Date.now(), text });
  }, []);

  const handleTabChange = useCallback(async (id: string) => {
    await switchTab(id);
    await refreshTabMetas();
  }, [refreshTabMetas, switchTab]);

  const handleTabClose = useCallback(async (id: string) => {
    await closeTab(id);
    await refreshTabMetas();
  }, [closeTab, refreshTabMetas]);

  const handleNewTab = useCallback(async () => {
    await pickWorkspace();
    await refreshTabMetas();
  }, [pickWorkspace, refreshTabMetas]);

  const handleOpenTopic = useCallback(async (scope: string, workspaceRoot: string, topicId: string) => {
    if (scope === "global") {
      await openGlobalTab(topicId);
    } else {
      await openProjectTab(workspaceRoot, topicId);
    }
    await refreshTabMetas();
  }, [openGlobalTab, openProjectTab, refreshTabMetas]);

  // History drawer: opening fetches the saved-session list. Idle row clicks resume;
  // running row clicks only preview through PreviewSession.
  const openHistory = useCallback(async () => {
    setHistView({ kind: "history", sessions: await listSessions() });
  }, [listSessions]);
  const openTrash = useCallback(async () => {
    setHistView({ kind: "trash", sessions: await listTrashedSessions() });
  }, [listTrashedSessions]);
  const closeHistory = useCallback(() => setHistView(null), []);
  const onResumeSession = useCallback(
    async (path: string) => {
      if (state.running) return;
      setHistView(null);
      await resumeSession(path);
    },
    [state.running, resumeSession],
  );
  // Delete / rename act on disk, then re-fetch so the panel reflects the change.
  const onDeleteSession = useCallback(
    async (path: string) => {
      if (state.running) return;
      await deleteSession(path);
      const sessions = await listSessions();
      setHistView((cur) => (cur === null ? null : cur.kind === "history" ? { ...cur, sessions } : cur));
    },
    [state.running, deleteSession, listSessions],
  );
  const onRenameSession = useCallback(
    async (path: string, title: string) => {
      if (state.running) return;
      await renameSession(path, title);
      const sessions = await listSessions();
      setHistView((cur) => (cur === null ? null : cur.kind === "history" ? { ...cur, sessions } : cur));
    },
    [state.running, renameSession, listSessions],
  );
  const onRestoreTrashedSession = useCallback(
    async (path: string) => {
      await restoreSession(path);
      const trashed = await listTrashedSessions();
      setHistView((cur) => (cur === null ? null : { kind: "trash", sessions: trashed }));
    },
    [restoreSession, listTrashedSessions],
  );
  const onPurgeTrashedSession = useCallback(
    async (path: string) => {
      await purgeTrashedSession(path);
      const trashed = await listTrashedSessions();
      setHistView((cur) => (cur === null ? null : { kind: "trash", sessions: trashed }));
    },
    [purgeTrashedSession, listTrashedSessions],
  );

  // Workspace: open the folder chooser and switch projects. The hook resets the
  // transcript and refreshes meta on a pick. A cancel is a no-op.
  const switchFolder = useCallback(async (path?: string) => {
    const picked = path === undefined ? await pickWorkspace() : await switchWorkspace(path);
    if (picked) {
      await refreshTabMetas();
    }
    return picked;
  }, [pickWorkspace, switchWorkspace, refreshTabMetas]);

  const removeWorkspace = useCallback(async (path: string) => {
    await app.RemoveWorkspace(path);
    await refreshTabMetas();
  }, [refreshTabMetas]);

  const onRemember = useCallback(
    async (scope: string, note: string) => {
      await remember(scope, note);
      setMemView(await fetchMemory());
    },
    [remember, fetchMemory],
  );

  const onForget = useCallback(
    async (name: string) => {
      await forget(name);
      setMemView(await fetchMemory());
    },
    [forget, fetchMemory],
  );

  const onSaveDoc = useCallback(
    async (path: string, body: string) => {
      await saveDoc(path, body);
      setMemView(await fetchMemory());
    },
    [saveDoc, fetchMemory],
  );

  const sidebarExpandBlocked = false;
  const sidebarToggleTitle = sidebarCollapsed
      ? t("sidebar.expand")
      : t("sidebar.collapse");

  return (
    <div className="app">
      <div
        className={[
          "layout",
          sidebarCollapsed ? "layout--sidebar-collapsed" : "",
          sidebarResizing ? "layout--resizing layout--sidebar-resizing" : "",
          workspacePanelOpen ? "layout--workspace-open" : "",
          workspacePanelOpen && workspacePanelMaximized ? "layout--workspace-maximized" : "",
          workspacePanelResizing ? "layout--resizing layout--workspace-resizing" : "",
        ]
          .filter(Boolean)
          .join(" ")}
        style={layoutStyle}
      >
        <header className="app-chrome">
          <button
            className={[
              "app-chrome__panel-toggle",
              "app-chrome__panel-toggle--left",
              !sidebarCollapsed ? "app-chrome__panel-toggle--active" : "",
              sidebarExpandBlocked ? "app-chrome__panel-toggle--blocked" : "",
            ].filter(Boolean).join(" ")}
            type="button"
            onClick={sidebarExpandBlocked ? undefined : toggleSidebar}
            aria-label={sidebarToggleTitle}
            aria-disabled={sidebarExpandBlocked}
          >
            {sidebarCollapsed ? <PanelLeftOpen size={15} /> : <PanelLeftClose size={15} />}
          </button>
          <div className="app-chrome__identity" aria-label="Reasonix">
            <img src={logo} alt="" className="app-chrome__logo" />
            <strong>Reasonix</strong>
            <span className="app-chrome__separator">/</span>
            <span className="app-chrome__scope">{appChromeScopeLabel(activeTab, state.meta)}</span>
          </div>
          <div className="app-chrome__spacer" />
          <button
            className={`app-chrome__panel-toggle app-chrome__panel-toggle--right${workspacePanelOpen ? " app-chrome__panel-toggle--active" : ""}`}
            type="button"
            onClick={toggleWorkspacePanel}
            aria-label={workspacePanelOpen ? "关闭右侧工作台" : "打开右侧工作台"}
            aria-pressed={workspacePanelOpen}
          >
            {workspacePanelOpen ? <PanelRightClose size={15} /> : <PanelRightOpen size={15} />}
          </button>
        </header>

        <aside className={`sidebar${sidebarCollapsed ? " sidebar--collapsed" : ""}`} aria-label={t("sidebar.navigation")}>
          <Tooltip label={t("topbar.newSession")} fill>
            <button
              className="sidebar__new"
              onClick={() => {
                if (state.running) cancel();
                void startNewSession();
              }}
            >
              <SquarePen size={15} />
              <span>{t("topbar.newSession")}</span>
            </button>
          </Tooltip>

          <section className="sidebar__section sidebar__section--projects">
            <ProjectTree
              activeScope={activeTab?.scope}
              activeWorkspaceRoot={activeTab?.workspaceRoot}
              activeTopicId={activeTab?.topicId}
              currentWorkspaceName={workspaceDisplayName(state.meta?.cwd)}
              onOpenTopic={(scope, workspaceRoot, topicId) => void handleOpenTopic(scope, workspaceRoot, topicId)}
              onAddProject={async () => {
                await switchFolder();
              }}
              onUseCurrentProject={state.meta?.cwd ? async () => {
                await switchFolder(state.meta?.cwd);
              } : undefined}
            />
          </section>

          <nav className="sidebar__nav">
            <Tooltip label={t("sidebar.trash")} fill>
              <button
                className="sidebar__navitem"
                onClick={() => void openTrash()}
              >
                <Trash2 size={15} />
                <span>{t("sidebar.trash")}</span>
              </button>
            </Tooltip>
            <Tooltip label={t("topbar.memory")} fill>
              <button className="sidebar__navitem" onClick={() => void openMemory()}>
                <Brain size={15} />
                <span>{t("topbar.memory")}</span>
              </button>
            </Tooltip>
            <Tooltip label={t("caps.title")} fill>
              <button className="sidebar__navitem" onClick={() => setCapsOpen(true)}>
                <Blocks size={15} />
                <span>{t("caps.title")}</span>
              </button>
            </Tooltip>
            <Tooltip label={t("topbar.settings")} fill>
              <button
                className="sidebar__navitem"
                onClick={() => setSettingsOpen(true)}
              >
                <SettingsIcon size={15} />
                <span>{t("topbar.settings")}</span>
              </button>
            </Tooltip>
          </nav>

        </aside>
        <button
          className="sidebar-resizer"
          type="button"
          role="separator"
          aria-orientation="vertical"
          aria-label={t("sidebar.resize")}
          aria-valuemin={SIDEBAR_MIN_WIDTH}
          aria-valuemax={SIDEBAR_MAX_WIDTH}
          aria-valuenow={sidebarWidth}
          onPointerDown={startSidebarResize}
          onKeyDown={resizeSidebarWithKeyboard}
          onDoubleClick={() => setExpandedSidebarWidth(SIDEBAR_DEFAULT_WIDTH)}
        />

        <section className="chat-pane">
          <header className="workspace-tabs-bar">
            <TabBar
              tabs={tabMetas}
              activeTabId={activeTabId}
              onTabChange={(id) => void handleTabChange(id)}
              onTabClose={(id) => void handleTabClose(id)}
              onNewTab={() => void handleNewTab()}
            />
          </header>

          <header className="topicbar">
            <div className="topicbar__identity">
              <div className="topicbar__title-row">
                <h1>{topicTitle(activeTab)}</h1>
                <Tooltip label="重命名会话">
                  <button className="topicbar__icon-btn">
                    <Pencil size={14} />
                  </button>
                </Tooltip>
              </div>
              <div className="topicbar__meta">
                <span>{state.meta?.label ?? "…"}</span>
                <span className="topicbar__context-badge">{formatContextWindow(state.context.window)}</span>
              </div>
            </div>
            <div className="topicbar__spacer" />
            <div className="topicbar__actions">
              <Tooltip label="固定会话">
                <button className="topicbar__icon-btn">
                  <Pin size={15} />
                </button>
              </Tooltip>
              <Tooltip label={t("topbar.history")}>
                <button className="topicbar__icon-btn" onClick={() => void openHistory()}>
                  <History size={15} />
                </button>
              </Tooltip>
              <Tooltip label="更多">
                <button className="topicbar__icon-btn">
                  <MoreHorizontal size={16} />
                </button>
              </Tooltip>
            </div>
          </header>

          {state.meta?.startupErr && (
            <div className="banner banner--error">{t("topbar.startupError", { msg: state.meta.startupErr })}</div>
          )}

          <UpdateBanner />

          <main className="main">
            {state.meta?.ready === false && !state.meta?.startupErr ? (
              <div className="loading-screen">
                <div className="loading-screen__spinner" />
                <span className="loading-screen__text">{t("common.loading")}</span>
              </div>
            ) : (
              <Transcript items={state.items} live={state.live} footerHeight={footerHeight} onPrompt={send} onRewind={rewind} />
            )}
          </main>

          <footer className="footer" ref={footerRef}>
            {showTodos && <TodoPanel todos={todos} onDismiss={() => setDismissedTodo(todoItem!.id)} />}
            {state.approval && (
              <ApprovalModal
                approval={state.approval}
                onAnswer={(allow, session, persist) => {
                  // Approving an exit_plan_mode plan leaves plan mode (the controller
                  // flips the executor; mirror it here for the indicator).
                  if (state.approval!.tool === "exit_plan_mode" && allow) setMode("normal");
                  approve(state.approval!.id, allow, session, persist);
                }}
                onRevisePlan={(text) => {
                  setPendingPlanRevision(text);
                  approve(state.approval!.id, false, false, false);
                }}
                onExitPlan={() => {
                  applyMode("normal");
                  approve(state.approval!.id, false, false, false);
                }}
              />
            )}
            {state.ask && (
              <AskCard
                ask={state.ask}
                onAnswer={answerQuestion}
                onDismiss={() => answerQuestion(state.ask!.id, [])}
              />
            )}
            <Composer
              running={state.running}
              mode={mode}
              cwd={state.meta?.cwd}
              modelLabel={state.meta?.label ?? t("status.connecting")}
              effort={state.effort}
              onSend={handleSend}
              onCancel={cancel}
              onCycleMode={cycleMode}
              onSetMode={applyMode}
              onSwitchModel={switchModel}
              onSetEffort={setEffort}
              onPickFolder={switchFolder}
              onRemoveWorkspace={removeWorkspace}
              insertRequest={composerInsertRequest}
              disabled={state.meta?.ready === false || state.approval != null || state.ask != null}
              ready={state.meta?.ready === true}
              turnStartAt={state.turnStartAt}
              turnTokens={state.turnTokens}
              retry={state.retry}
            />
            <StatusBar
              context={state.context}
              usage={state.usage}
              balance={state.balance}
              jobs={state.jobs}
              running={state.running}
              mode={mode}
              cost={state.sessionCostUsd}
            />
          </footer>
        </section>

        {workspacePanelOpen && !workspacePanelMaximized && (
          <button
            className="workspace-panel-resizer"
            type="button"
            role="separator"
            aria-orientation="vertical"
            aria-label="调整右侧面板宽度"
            aria-valuemin={WORKSPACE_PANEL_MIN_WIDTH}
            aria-valuemax={WORKSPACE_PANEL_MAX_WIDTH}
            aria-valuenow={effectiveWorkspacePanelWidth}
            onPointerDown={startWorkspacePanelResize}
            onKeyDown={resizeWorkspacePanelWithKeyboard}
            onDoubleClick={() => setSavedWorkspacePanelWidth(WORKSPACE_PANEL_DEFAULT_WIDTH)}
          />
        )}

        {workspacePanelOpen && (
          <aside className={`workbench-dock workbench-dock--${rightDockMode}`} aria-label="右侧工作台">
            <div className="workbench-dock__tools">
              <div className="workbench-dock__tabs" role="tablist" aria-label="右侧工作台视图">
                {SHOW_CONTEXT_DOCK && (
                  <button
                    type="button"
                    role="tab"
                    aria-selected={rightDockMode === "context"}
                    className={`workbench-dock__tab${rightDockMode === "context" ? " workbench-dock__tab--active" : ""}`}
                    onClick={() => openRightDockMode("context")}
                  >
                    <CircleGauge size={13} />
                    概览
                  </button>
                )}
                <button
                  type="button"
                  role="tab"
                  aria-selected={rightDockMode === "files"}
                  className={`workbench-dock__tab${rightDockMode === "files" ? " workbench-dock__tab--active" : ""}`}
                  onClick={() => openRightDockMode("files")}
                >
                  <FileText size={13} />
                  {t("workspace.filesTab")}
                </button>
                <button
                  type="button"
                  role="tab"
                  aria-selected={rightDockMode === "changed"}
                  className={`workbench-dock__tab${rightDockMode === "changed" ? " workbench-dock__tab--active" : ""}`}
                  onClick={() => openRightDockMode("changed")}
                >
                  <GitBranch size={13} />
                  {t("workspace.changedTab")}
                </button>
              </div>
              <Tooltip label="刷新右侧视图">
                <button
                  className="workspace-iconbtn"
                  type="button"
                  onClick={() => setDockRefreshKey((key) => key + 1)}
                >
                  <RefreshCw size={14} />
                </button>
              </Tooltip>
            </div>
            <div className="workbench-dock__body">
              {rightDockMode === "context" ? (
                <ContextPanel
                  tabId={activeTabId}
                  context={state.context}
                  usage={state.usage}
                  sessionCostUsd={state.sessionCostUsd}
                  scopeLabel={topicScopeLabel(activeTab)}
                  refreshKey={dockRefreshKey}
                />
              ) : (
                <WorkspacePanel
                  open={workspacePanelOpen}
                  cwd={state.meta?.cwd}
                  maximized={workspacePanelMaximized}
                  panelWidth={effectiveWorkspacePanelWidth}
                  onClose={() => setWorkspacePanel(false)}
                  onToggleMaximized={() => setWorkspacePanelMaximized((value) => !value)}
                  onAddToChat={addWorkspaceTextToComposer}
                  refreshKey={dockRefreshKey}
                  initialViewMode={rightDockMode === "changed" ? "changed" : "files"}
                  showViewTabs={false}
                />
              )}
            </div>
          </aside>
        )}
      </div>

      {memView !== null && (
        <MemoryPanel
          view={memView}
          onClose={closeMemory}
          onRemember={onRemember}
          onForget={onForget}
          onSaveDoc={onSaveDoc}
        />
      )}

      {histView !== null && (
        <HistoryPanel
          kind={histView.kind}
          sessions={histView.sessions}
          running={state.running}
          onResume={onResumeSession}
          onPreview={previewSession}
          onDelete={onDeleteSession}
          onRename={onRenameSession}
          onRestore={onRestoreTrashedSession}
          onPurge={onPurgeTrashedSession}
          onClose={closeHistory}
        />
      )}

      {settingsOpen && <SettingsPanel onClose={() => setSettingsOpen(false)} onChanged={() => void refreshMeta()} />}

      {capsOpen && <CapabilitiesPanel onClose={() => setCapsOpen(false)} />}

      {needsOnboarding && <OnboardingOverlay onComplete={() => setNeedsOnboarding(false)} />}
    </div>
  );
}
