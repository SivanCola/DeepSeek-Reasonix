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
import { app, onProjectTreeChanged } from "./lib/bridge";
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
import { FileTabPane } from "./components/FileTabPane";
import { Tooltip } from "./components/Tooltip";
import { OnboardingOverlay } from "./components/OnboardingOverlay";
import { TabBar } from "./components/TabBar";
import { ProjectTree } from "./components/ProjectTree";
import { parseTodos } from "./lib/tools";
import type { ComposerInsertRequest, MemoryView, Meta, Mode, SessionMeta, TabMeta } from "./lib/types";
import { loadLayoutSize, saveLayoutSize } from "./lib/layoutPreferences";
import { applyTheme, getTheme, getThemeStyle, isThemeStyle, themeForStyle, type Theme } from "./lib/theme";
import {
  ScreenGetAll,
  WindowGetPosition,
  WindowGetSize,
  WindowIsFullscreen,
  WindowIsMaximised,
  WindowSetSize,
} from "../wailsjs/runtime/runtime";

const SIDEBAR_COLLAPSED_KEY = "reasonix.sidebar.collapsed";
const SIDEBAR_DEFAULT_WIDTH = 264;
const SIDEBAR_MIN_WIDTH = 228;
const SIDEBAR_MAX_WIDTH = 320;
const CHAT_MIN_WIDTH = 760;

function isThemeMode(value: string): value is Theme {
  return value === "auto" || value === "light" || value === "dark";
}
const CONTEXT_PANEL_MIN_WIDTH = 340;
const CONTEXT_PANEL_MAX_WIDTH = 420;
const RIGHT_DOCK_MIN_WIDTH = CONTEXT_PANEL_MIN_WIDTH;
const RIGHT_DOCK_CONTEXT_WIDTH = 380;
const RIGHT_DOCK_DEFAULT_WIDTH = 420;
const RIGHT_DOCK_MAX_WIDTH = 860;
const RIGHT_DOCK_RESIZER_WIDTH = 8;

type RightDockMode = "context" | "files" | "changed";
const SHOW_CONTEXT_DOCK = false;
type HistoryScopeFilter = { scope: "global" | "project"; workspaceRoot: string };
type HistoryViewState =
  | { kind: "history"; source: "scope"; filter: HistoryScopeFilter; sessions: SessionMeta[] }
  | { kind: "history"; source: "all"; sessions: SessionMeta[] }
  | { kind: "trash"; sessions: SessionMeta[] };
type WailsScreen = { isCurrent: boolean; isPrimary: boolean; width: number; height: number };

function hasNativeWindowRuntime(): boolean {
  if (typeof window === "undefined") return false;
  const runtime = (window as { runtime?: Record<string, unknown> }).runtime;
  return typeof runtime?.WindowGetSize === "function" && typeof runtime.WindowSetSize === "function";
}

function clampSidebarWidth(width: number): number {
  return Math.min(SIDEBAR_MAX_WIDTH, Math.max(SIDEBAR_MIN_WIDTH, Math.round(width)));
}

function clampRightDockWidth(width: number): number {
  return Math.min(RIGHT_DOCK_MAX_WIDTH, Math.max(RIGHT_DOCK_MIN_WIDTH, Math.round(width)));
}

function resolveRightDockSplit(totalWidth: number, desiredDockWidth: number): { chatWidth: number; dockWidth: number } {
  const total = Math.round(totalWidth);
  const maxDockWidth = Math.max(RIGHT_DOCK_MIN_WIDTH, Math.min(RIGHT_DOCK_MAX_WIDTH, total - CHAT_MIN_WIDTH));
  const dockWidth = Math.min(maxDockWidth, Math.max(RIGHT_DOCK_MIN_WIDTH, Math.round(desiredDockWidth)));
  return {
    chatWidth: Math.max(CHAT_MIN_WIDTH, total - dockWidth),
    dockWidth,
  };
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

function loadRightDockWidth(): number {
  return loadLayoutSize("rightDockWidth", RIGHT_DOCK_DEFAULT_WIDTH, clampRightDockWidth);
}

function saveRightDockWidth(width: number): void {
  saveLayoutSize("rightDockWidth", width, clampRightDockWidth);
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

type FileTabMeta = TabMeta & {
  tabType: "file";
  scope: "file";
  filePath: string;
};

function fileTabId(workspaceRoot: string, path: string): string {
  return `file:${workspaceRoot || "global"}:${path}`;
}

function fileName(path: string): string {
  return path.split("/").filter(Boolean).pop() || path;
}

function sessionsForScope(sessions: SessionMeta[], filter: HistoryScopeFilter): SessionMeta[] {
  if (filter.scope === "project") {
    return sessions.filter((session) => session.scope === "project" && session.workspaceRoot === filter.workspaceRoot);
  }
  return sessions.filter((session) => (session.scope || "global") === "global");
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
    reorderTabs,
  } = useController();
  const t = useT();
  const [mode, setMode] = useState<Mode>("normal");
  const [tabMetas, setTabMetas] = useState<TabMeta[]>([]);
  const [fileTabs, setFileTabs] = useState<FileTabMeta[]>([]);
  const [activeFileTabId, setActiveFileTabId] = useState<string | null>(null);
  const [tabOrderIds, setTabOrderIds] = useState<string[]>([]);
  const [tabRevealSignal, setTabRevealSignal] = useState(0);
  // null until the mount probe resolves; true shows the overlay. Probed once —
  // clearing the key mid-session is the Settings panel's job, not the gate's.
  const [needsOnboarding, setNeedsOnboarding] = useState<boolean | null>(null);
  const [memView, setMemView] = useState<MemoryView | null>(null);
  const [histView, setHistView] = useState<HistoryViewState | null>(null);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(loadSidebarCollapsed);
  const [sidebarWidth, setSidebarWidth] = useState(loadSidebarWidth);
  const [sidebarResizing, setSidebarResizing] = useState(false);
  const [workspacePanelOpen, setWorkspacePanelOpen] = useState(true);
  const [rightDockWidth, setRightDockWidth] = useState(loadRightDockWidth);
  const [workspacePanelResizing, setWorkspacePanelResizing] = useState(false);
  const [workspacePanelMaximized, setWorkspacePanelMaximized] = useState(false);
  const [rightDockMode, setRightDockMode] = useState<RightDockMode>("files");
  const [dockRefreshKey, setDockRefreshKey] = useState(0);
  const [projectRevision, setProjectRevision] = useState(0);
  const [composerInsertRequest, setComposerInsertRequest] = useState<ComposerInsertRequest | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [capsOpen, setCapsOpen] = useState(false);
  const [pendingPlanRevision, setPendingPlanRevision] = useState<string | null>(null);
  const [footerHeight, setFooterHeight] = useState(0);
  const footerRef = useRef<HTMLElement>(null);
  const chatPaneRef = useRef<HTMLElement>(null);
  const workbenchDockRef = useRef<HTMLElement>(null);
  const rightDockToggleSequenceRef = useRef(0);
  const rightDockTogglingRef = useRef(false);
  const [chatWidth, setChatWidth] = useState<number | null>(null);
  const [rightDockRenderWidth, setRightDockRenderWidth] = useState<number | null>(null);
  const preferredWorkspacePanelWidth =
    rightDockMode === "context" ? Math.min(CONTEXT_PANEL_MAX_WIDTH, rightDockWidth) : rightDockWidth;
  const workspacePanelRenderWidth = rightDockRenderWidth ?? preferredWorkspacePanelWidth;
  const activeTab = useMemo(
    () => tabMetas.find((tab) => tab.id === activeTabId) ?? tabMetas.find((tab) => tab.active),
    [activeTabId, tabMetas],
  );
  const activeFileTab = useMemo(
    () => fileTabs.find((tab) => tab.id === activeFileTabId),
    [activeFileTabId, fileTabs],
  );
  const visibleTabId = activeFileTabId ?? activeTabId;
  const visibleTabs = useMemo(() => {
    const source = [...tabMetas, ...fileTabs];
    const byId = new Map(source.map((tab) => [tab.id, tab]));
    const ordered = tabOrderIds.map((id) => byId.get(id)).filter((tab): tab is TabMeta => Boolean(tab));
    const missing = source.filter((tab) => !tabOrderIds.includes(tab.id));
    return [...ordered, ...missing].map((tab) => ({
      ...tab,
      active: tab.id === visibleTabId,
    }));
  }, [activeFileTabId, activeTabId, fileTabs, tabMetas, tabOrderIds, visibleTabId]);

  useEffect(() => {
    const ids = [...tabMetas.map((tab) => tab.id), ...fileTabs.map((tab) => tab.id)];
    setTabOrderIds((current) => {
      const next = current.filter((id) => ids.includes(id));
      for (const id of ids) {
        if (!next.includes(id)) next.push(id);
      }
      return next.join("\u0000") === current.join("\u0000") ? current : next;
    });
  }, [fileTabs, tabMetas]);

  useEffect(() => {
    if (activeFileTabId && !fileTabs.some((tab) => tab.id === activeFileTabId)) {
      setActiveFileTabId(null);
    }
  }, [activeFileTabId, fileTabs]);

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
    return onProjectTreeChanged(() => {
      setProjectRevision((value) => value + 1);
      void refreshTabMetas();
    });
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
    const el = footerRef.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const update = () => setFooterHeight(Math.round(el.getBoundingClientRect().height));
    update();
    const observer = new ResizeObserver(update);
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  const measureChatWidth = useCallback(() => {
    const width = chatPaneRef.current?.getBoundingClientRect().width;
    return Math.max(CHAT_MIN_WIDTH, Math.round(width && Number.isFinite(width) ? width : CHAT_MIN_WIDTH));
  }, []);

  const measureRightDockWidth = useCallback(() => {
    const width = workbenchDockRef.current?.getBoundingClientRect().width;
    if (width && Number.isFinite(width)) return Math.max(RIGHT_DOCK_MIN_WIDTH, Math.round(width));
    return preferredWorkspacePanelWidth;
  }, [preferredWorkspacePanelWidth]);

  useEffect(() => {
    if (!workspacePanelOpen || workspacePanelMaximized) {
      setRightDockRenderWidth(null);
      return;
    }
    const el = workbenchDockRef.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const update = () => {
      const width = el.getBoundingClientRect().width;
      if (width && Number.isFinite(width)) setRightDockRenderWidth(Math.max(RIGHT_DOCK_MIN_WIDTH, Math.round(width)));
    };
    update();
    const observer = new ResizeObserver(update);
    observer.observe(el);
    return () => observer.disconnect();
  }, [workspacePanelMaximized, workspacePanelOpen]);

  useEffect(() => {
    if (workspacePanelMaximized || chatWidth !== null) return;
    const frame = window.requestAnimationFrame(() => {
      setChatWidth(measureChatWidth());
    });
    return () => window.cancelAnimationFrame(frame);
  }, [chatWidth, measureChatWidth, workspacePanelMaximized]);

  const startNewSession = useCallback(async () => {
    setActiveFileTabId(null);
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

  const resizeNativeWindowBy = useCallback(
    async (deltaWidth: number): Promise<boolean> => {
      if (!hasNativeWindowRuntime()) return false;
      try {
        const [fullscreen, maximised] = await Promise.all([WindowIsFullscreen(), WindowIsMaximised()]);
        if (fullscreen || maximised) return false;

        const [size, position, screens] = await Promise.all([WindowGetSize(), WindowGetPosition(), ScreenGetAll()]);
        const desiredWidth = Math.max(640, Math.round(size.w + deltaWidth));
        const screenList = screens as WailsScreen[];
        const screen = screenList.find((item) => item.isCurrent) ?? screenList.find((item) => item.isPrimary) ?? screenList[0];
        const availableRight = screen && position.x >= 0 ? screen.width - position.x : screen?.width;
        const maxWidth = availableRight && availableRight > size.w ? availableRight : desiredWidth;
        const nextWidth = deltaWidth > 0 ? Math.min(desiredWidth, maxWidth) : desiredWidth;
        if (Math.abs(nextWidth - size.w) > 8) {
          WindowSetSize(nextWidth, size.h);
          return deltaWidth <= 0 || nextWidth >= desiredWidth - 8;
        }
      } catch {
        /* The browser dev runtime and some window states do not expose sizing. */
      }
      return false;
    },
    [],
  );

  const setSavedWorkspacePanelWidth = useCallback(
    (width: number) => {
      const currentChatWidth = chatWidth ?? measureChatWidth();
      const currentDockWidth = measureRightDockWidth();
      const next = resolveRightDockSplit(currentChatWidth + currentDockWidth, width);
      setRightDockWidth(next.dockWidth);
      saveRightDockWidth(next.dockWidth);
      if (workspacePanelOpen && !workspacePanelMaximized) {
        setChatWidth(next.chatWidth);
      }
    },
    [chatWidth, measureChatWidth, measureRightDockWidth, workspacePanelMaximized, workspacePanelOpen],
  );

  const ensureWorkspacePanelWidth = useCallback(
    (width: number) => {
      const desiredDockWidth = clampRightDockWidth(width);
      const currentChatWidth = chatWidth ?? measureChatWidth();
      const currentDockWidth = measureRightDockWidth();
      const missingWidth = desiredDockWidth - currentDockWidth;

      if (!workspacePanelOpen || workspacePanelMaximized || missingWidth <= 8) {
        setSavedWorkspacePanelWidth(desiredDockWidth);
        return;
      }

      void (async () => {
        const resized = await resizeNativeWindowBy(missingWidth);
        if (resized) {
          setRightDockWidth(desiredDockWidth);
          saveRightDockWidth(desiredDockWidth);
          setChatWidth(currentChatWidth);
          return;
        }
        setSavedWorkspacePanelWidth(desiredDockWidth);
      })();
    },
    [
      chatWidth,
      measureChatWidth,
      measureRightDockWidth,
      resizeNativeWindowBy,
      setSavedWorkspacePanelWidth,
      workspacePanelMaximized,
      workspacePanelOpen,
    ],
  );

  const startWorkspacePanelResize = useCallback(
    (event: ReactPointerEvent<HTMLButtonElement>) => {
      if (!workspacePanelOpen) return;
      event.preventDefault();
      setWorkspacePanelResizing(true);
      const startX = event.clientX;
      const startChatWidth = chatWidth ?? measureChatWidth();
      const startDockWidth = measureRightDockWidth();
      const totalWidth = startChatWidth + startDockWidth;
      let nextChatWidth = startChatWidth;
      let nextDockWidth = startDockWidth;
      const onMove = (moveEvent: PointerEvent) => {
        const delta = moveEvent.clientX - startX;
        const next = resolveRightDockSplit(totalWidth, startDockWidth - delta);
        nextChatWidth = next.chatWidth;
        nextDockWidth = next.dockWidth;
        setChatWidth(nextChatWidth);
        setRightDockWidth(nextDockWidth);
      };
      const onDone = () => {
        setChatWidth(nextChatWidth);
        setRightDockWidth(nextDockWidth);
        saveRightDockWidth(nextDockWidth);
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
    [chatWidth, measureChatWidth, measureRightDockWidth, workspacePanelOpen],
  );

  const resizeWorkspacePanelWithKeyboard = useCallback(
    (event: KeyboardEvent<HTMLButtonElement>) => {
      if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
        event.preventDefault();
        setSavedWorkspacePanelWidth(measureRightDockWidth() + (event.key === "ArrowLeft" ? 16 : -16));
      } else if (event.key === "Home") {
        event.preventDefault();
        setSavedWorkspacePanelWidth(RIGHT_DOCK_MIN_WIDTH);
      } else if (event.key === "End") {
        event.preventDefault();
        setSavedWorkspacePanelWidth(RIGHT_DOCK_MAX_WIDTH);
      }
    },
    [measureRightDockWidth, setSavedWorkspacePanelWidth],
  );

  const openWorkspacePanel = useCallback(
    (mode: RightDockMode = rightDockMode) => {
      setRightDockMode(mode);
      if (mode === "context") {
        setWorkspacePanelMaximized(false);
      }
      if (workspacePanelOpen) {
        return;
      }
      if (rightDockTogglingRef.current) {
        return;
      }

      const sequence = rightDockToggleSequenceRef.current + 1;
      rightDockToggleSequenceRef.current = sequence;
      const capturedChatWidth = measureChatWidth();
      const targetWidth = mode === "context" ? Math.min(CONTEXT_PANEL_MAX_WIDTH, rightDockWidth) : rightDockWidth;
      rightDockTogglingRef.current = true;
      setChatWidth(capturedChatWidth);
      setWorkspacePanelOpen(true);
      void (async () => {
        const resized = await resizeNativeWindowBy(targetWidth + RIGHT_DOCK_RESIZER_WIDTH);
        if (rightDockToggleSequenceRef.current !== sequence) return;
        if (!resized) {
          setChatWidth(null);
        }
        rightDockTogglingRef.current = false;
      })();
    },
    [measureChatWidth, resizeNativeWindowBy, rightDockMode, rightDockWidth, workspacePanelOpen],
  );

  const closeWorkspacePanel = useCallback(() => {
    if (!workspacePanelOpen) {
      return;
    }
    if (rightDockTogglingRef.current) {
      return;
    }

    const sequence = rightDockToggleSequenceRef.current + 1;
    rightDockToggleSequenceRef.current = sequence;
    const dockWidth = measureRightDockWidth();
    rightDockTogglingRef.current = true;
    setWorkspacePanelMaximized(false);
    void (async () => {
      await resizeNativeWindowBy(-(dockWidth + RIGHT_DOCK_RESIZER_WIDTH));
      if (rightDockToggleSequenceRef.current !== sequence) return;
      setWorkspacePanelOpen(false);
      setRightDockRenderWidth(null);
      setChatWidth(null);
      rightDockTogglingRef.current = false;
    })();
  }, [measureRightDockWidth, resizeNativeWindowBy, workspacePanelOpen]);

  const openRightDockMode = useCallback(
    (mode: RightDockMode) => {
      openWorkspacePanel(mode);
    },
    [openWorkspacePanel],
  );

  const layoutStyle = useMemo(
    () =>
      ({
        "--sidebar-expanded-width": `${sidebarWidth}px`,
        "--chat-min-width": `${CHAT_MIN_WIDTH}px`,
        "--chat-width": `${chatWidth ?? CHAT_MIN_WIDTH}px`,
        "--workspace-width": `${preferredWorkspacePanelWidth}px`,
      }) as CSSProperties,
    [chatWidth, preferredWorkspacePanelWidth, sidebarWidth],
  );

  const setWorkspacePanel = useCallback((open: boolean) => {
    if (open) {
      openWorkspacePanel();
    } else {
      closeWorkspacePanel();
    }
  }, [closeWorkspacePanel, openWorkspacePanel]);

  const toggleWorkspacePanel = useCallback(() => {
    if (workspacePanelOpen) {
      closeWorkspacePanel();
      return;
    }
    openWorkspacePanel();
  }, [closeWorkspacePanel, openWorkspacePanel, workspacePanelOpen]);

  const addWorkspaceTextToComposer = useCallback((text: string) => {
    setActiveFileTabId(null);
    setComposerInsertRequest({ id: Date.now(), text });
  }, []);

  const openWorkspaceFileTab = useCallback((path: string) => {
    const workspaceRoot = state.meta?.cwd ?? "";
    const workspaceName = workspaceDisplayName(workspaceRoot) || "Project";
    const id = fileTabId(workspaceRoot, path);
    setFileTabs((current) => {
      if (current.some((tab) => tab.id === id)) return current;
      return [
        ...current,
        {
          id,
          tabType: "file",
          scope: "file",
          workspaceRoot,
          workspaceName,
          topicId: "",
          topicTitle: fileName(path),
          filePath: path,
          label: "File",
          ready: true,
          running: false,
          active: false,
          cwd: workspaceRoot,
        },
      ];
    });
    setTabOrderIds((current) => (current.includes(id) ? current : [...current, id]));
    setActiveFileTabId(id);
    setTabRevealSignal((signal) => signal + 1);
  }, [state.meta?.cwd]);

  const handleTabChange = useCallback(async (id: string) => {
    if (fileTabs.some((tab) => tab.id === id)) {
      setActiveFileTabId(id);
      setTabRevealSignal((signal) => signal + 1);
      return;
    }
    setActiveFileTabId(null);
    await switchTab(id);
    await refreshTabMetas();
    setTabRevealSignal((signal) => signal + 1);
  }, [fileTabs, refreshTabMetas, switchTab]);

  const handleTabClose = useCallback(async (id: string) => {
    const closingFileTab = fileTabs.find((tab) => tab.id === id);
    if (closingFileTab) {
      const currentTabs = visibleTabs;
      const closingIndex = currentTabs.findIndex((tab) => tab.id === id);
      const remaining = currentTabs.filter((tab) => tab.id !== id);
      setFileTabs((current) => current.filter((tab) => tab.id !== id));
      setTabOrderIds((current) => current.filter((tabId) => tabId !== id));
      if (activeFileTabId === id) {
        const nextIndex = Math.min(Math.max(closingIndex, 0), remaining.length - 1);
        const nextTab = remaining[nextIndex];
        if (nextTab?.tabType === "file" || nextTab?.scope === "file") {
          setActiveFileTabId(nextTab.id);
        } else {
          setActiveFileTabId(null);
          if (nextTab?.id) await switchTab(nextTab.id);
        }
      }
      setTabRevealSignal((signal) => signal + 1);
      return;
    }
    setTabMetas((current) => {
      if (current.length <= 1) return current;
      const closingIndex = current.findIndex((tab) => tab.id === id);
      if (closingIndex < 0) return current;
      const closingTab = current[closingIndex];
      const remaining = current.filter((tab) => tab.id !== id);
      if (!closingTab.active && closingTab.id !== activeTabId) return remaining;
      const nextIndex = Math.min(closingIndex, remaining.length - 1);
      const nextActiveId = remaining[nextIndex]?.id;
      return remaining.map((tab) => ({ ...tab, active: tab.id === nextActiveId }));
    });
    if (activeFileTabId === id) setActiveFileTabId(null);
    await closeTab(id);
    await refreshTabMetas();
    setTabRevealSignal((signal) => signal + 1);
  }, [activeFileTabId, activeTabId, closeTab, fileTabs, refreshTabMetas, switchTab, visibleTabs]);

  const handleTabsReorder = useCallback(async (ids: string[]) => {
    setTabOrderIds(ids);
    setTabMetas((current) => {
      const byId = new Map(current.map((tab) => [tab.id, tab]));
      const ordered = ids.map((id) => byId.get(id)).filter((tab): tab is TabMeta => Boolean(tab));
      return ordered.length === current.length ? ordered : current;
    });
    setFileTabs((current) => {
      const byId = new Map(current.map((tab) => [tab.id, tab]));
      const ordered = ids.map((id) => byId.get(id)).filter((tab): tab is FileTabMeta => Boolean(tab));
      return ordered.length === current.length ? ordered : current;
    });
    const sessionIds = ids.filter((id) => tabMetas.some((tab) => tab.id === id));
    await reorderTabs(sessionIds);
    await refreshTabMetas();
    setTabRevealSignal((signal) => signal + 1);
  }, [refreshTabMetas, reorderTabs, tabMetas]);

  const handleNewTab = useCallback(async () => {
    setActiveFileTabId(null);
    await pickWorkspace();
    await refreshTabMetas();
    setTabRevealSignal((signal) => signal + 1);
  }, [pickWorkspace, refreshTabMetas]);

  const handleOpenTopic = useCallback(async (scope: string, workspaceRoot: string, topicId: string) => {
    setActiveFileTabId(null);
    if (scope === "global") {
      await openGlobalTab(topicId);
    } else {
      await openProjectTab(workspaceRoot, topicId);
    }
    await refreshTabMetas();
    setTabRevealSignal((signal) => signal + 1);
  }, [openGlobalTab, openProjectTab, refreshTabMetas]);

  // History drawer: project menus can open a scoped saved-session list. Idle row
  // clicks resume; running row clicks only preview through PreviewSession.
  const openProjectHistory = useCallback(async (scope: "global" | "project", workspaceRoot: string) => {
    const filter = { scope, workspaceRoot };
    setHistView({ kind: "history", source: "scope", filter, sessions: sessionsForScope(await listSessions(), filter) });
  }, [listSessions]);
  const openAllHistory = useCallback(async () => {
    setHistView({ kind: "history", source: "all", sessions: await listSessions() });
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
      setHistView((cur) =>
        cur === null
          ? null
          : cur.kind === "history"
            ? { ...cur, sessions: cur.source === "scope" ? sessionsForScope(sessions, cur.filter) : sessions }
            : cur,
      );
    },
    [state.running, deleteSession, listSessions],
  );
  const onRenameSession = useCallback(
    async (path: string, title: string) => {
      if (state.running) return;
      await renameSession(path, title);
      const sessions = await listSessions();
      setHistView((cur) =>
        cur === null
          ? null
          : cur.kind === "history"
            ? { ...cur, sessions: cur.source === "scope" ? sessionsForScope(sessions, cur.filter) : sessions }
            : cur,
      );
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
      setProjectRevision((value) => value + 1);
      await refreshTabMetas();
    }
    return picked;
  }, [pickWorkspace, switchWorkspace, refreshTabMetas]);

  const removeWorkspace = useCallback(async (path: string) => {
    await app.RemoveWorkspace(path);
    setProjectRevision((value) => value + 1);
    await refreshTabMetas();
  }, [refreshTabMetas]);

  const refreshProjectsAndTabs = useCallback(async () => {
    setProjectRevision((value) => value + 1);
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
  const workspacePanelResetWidth = rightDockMode === "context" ? RIGHT_DOCK_CONTEXT_WIDTH : RIGHT_DOCK_DEFAULT_WIDTH;

  return (
    <div className="app">
      <div
        className={[
          "layout",
          sidebarCollapsed ? "layout--sidebar-collapsed" : "",
          sidebarResizing ? "layout--resizing layout--sidebar-resizing" : "",
          workspacePanelOpen ? "layout--workspace-open" : "",
          workspacePanelOpen && !workspacePanelMaximized && chatWidth !== null ? "layout--chat-sized" : "",
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
              onOpenTopic={handleOpenTopic}
              onOpenProjectHistory={openProjectHistory}
              onTopicsChanged={() => void refreshProjectsAndTabs()}
              refreshSignal={projectRevision}
              onAddProject={async () => {
                await switchFolder();
              }}
              onUseCurrentProject={state.meta?.cwd ? async () => {
                await switchFolder(state.meta?.cwd);
              } : undefined}
            />
          </section>

          <nav className="sidebar__nav">
            <Tooltip label={t("sidebar.allHistory")} fill>
              <button
                className="sidebar__navitem"
                onClick={() => void openAllHistory()}
              >
                <History size={15} />
                <span>{t("sidebar.allHistory")}</span>
              </button>
            </Tooltip>
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

        <section className="chat-pane" ref={chatPaneRef}>
          <header className="workspace-tabs-bar">
            <TabBar
              tabs={visibleTabs}
              activeTabId={visibleTabId}
              revealActiveSignal={tabRevealSignal}
              onTabChange={(id) => void handleTabChange(id)}
              onTabClose={(id) => void handleTabClose(id)}
              onTabsReorder={(ids) => void handleTabsReorder(ids)}
              onNewTab={() => void handleNewTab()}
            />
          </header>

          {activeFileTab ? (
            <FileTabPane
              path={activeFileTab.filePath}
              workspaceName={activeFileTab.workspaceName}
              onAddToChat={addWorkspaceTextToComposer}
            />
          ) : (
          <>
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
              workspaceRefreshSignal={projectRevision}
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
          </>
          )}
        </section>

        {workspacePanelOpen && !workspacePanelMaximized && (
          <button
            className="workspace-panel-resizer"
            type="button"
            role="separator"
            aria-orientation="vertical"
            aria-label="调整右侧面板宽度"
            aria-valuemin={RIGHT_DOCK_MIN_WIDTH}
            aria-valuemax={Math.max(RIGHT_DOCK_MAX_WIDTH, workspacePanelRenderWidth)}
            aria-valuenow={workspacePanelRenderWidth}
            onPointerDown={startWorkspacePanelResize}
            onKeyDown={resizeWorkspacePanelWithKeyboard}
            onDoubleClick={() => setSavedWorkspacePanelWidth(workspacePanelResetWidth)}
          />
        )}

        {workspacePanelOpen && (
          <aside
            className={[
              "workbench-dock",
              `workbench-dock--${rightDockMode}`,
            ].join(" ")}
            ref={workbenchDockRef}
            aria-label="右侧工作台"
          >
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
                  panelWidth={workspacePanelRenderWidth}
                  onClose={() => setWorkspacePanel(false)}
                  onToggleMaximized={() => setWorkspacePanelMaximized((value) => !value)}
                  onAddToChat={addWorkspaceTextToComposer}
                  onOpenFileTab={openWorkspaceFileTab}
                  onRequestPanelWidth={ensureWorkspacePanelWidth}
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
