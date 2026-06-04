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
  Trash2,
} from "lucide-react";
import logo from "./assets/logo.svg";
import { asArray } from "./lib/array";
import { t, useT } from "./lib/i18n";
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
import { Tooltip } from "./components/Tooltip";
import { OnboardingOverlay } from "./components/OnboardingOverlay";
import { TabBar } from "./components/TabBar";
import { ProjectTree } from "./components/ProjectTree";
import { parseTodos } from "./lib/tools";
import type { ComposerInsertRequest, MemoryView, Meta, Mode, SessionMeta, TabMeta } from "./lib/types";
import { loadLayoutSize, saveLayoutSize } from "./lib/layoutPreferences";
import { applyTheme, getTheme, getThemeStyle, isThemeStyle, themeForStyle, type Theme } from "./lib/theme";
import { useWindowStatePersistence } from "./lib/windowState";
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
  if (!tab || tab.scope === "global") return t("scope.global");
  return t("scope.project", { name: tab.workspaceName || tab.workspaceRoot || "Project" });
}

function appChromeScopeLabel(tab?: TabMeta, meta?: Meta): string {
  if (tab?.scope === "project") return tab.workspaceName || tab.workspaceRoot || "Project";
  if (tab?.scope === "global") return tab.topicTitle || "Global";
  return workspaceDisplayName(meta?.cwd) || meta?.label || "Global";
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
  const [modesByTab, setModesByTab] = useState<Record<string, Mode>>({});
  const [tabMetas, setTabMetas] = useState<TabMeta[]>([]);
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
  const dockRefreshKey = 0;
  const [projectRevision, setProjectRevision] = useState(0);
  const [composerInsertRequest, setComposerInsertRequest] = useState<ComposerInsertRequest | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [capsOpen, setCapsOpen] = useState(false);
  const [renamingTopicId, setRenamingTopicId] = useState<string | null>(null);
  const [topicTitleDraft, setTopicTitleDraft] = useState("");
  const topicRenameSkipCommitRef = useRef(false);
  const topicRenameCommitHandledRef = useRef(false);

  // Persist window geometry across launches.
  useWindowStatePersistence();

  // Open settings when the native menu item (CmdOrCtrl+,) is activated.
  useEffect(() => {
    if (typeof window === "undefined" || !window.runtime) return;
    return window.runtime.EventsOn("app:open-settings", () => {
      setSettingsOpen(true);
    });
  }, []);
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
  const mode = activeTabId ? modesByTab[activeTabId] ?? "normal" : "normal";
  const setMode = useCallback(
    (next: Mode | ((prev: Mode) => Mode)) => {
      if (!activeTabId) return;
      setModesByTab((current) => {
        const prev = current[activeTabId] ?? "normal";
        const value = typeof next === "function" ? next(prev) : next;
        if (value === prev) return current;
        return { ...current, [activeTabId]: value };
      });
    },
    [activeTabId],
  );
  const topicbarEditing = Boolean(activeTab?.topicId && activeTab.topicId === renamingTopicId);
  const topicbarProjectPrefix = activeTab?.scope === "project"
    ? activeTab.workspaceName || activeTab.workspaceRoot || "Project"
    : "";
  const visibleTabId = activeTabId;
  const visibleTabs = useMemo(() => {
    const byId = new Map(tabMetas.map((tab) => [tab.id, tab]));
    const ordered = tabOrderIds.map((id) => byId.get(id)).filter((tab): tab is TabMeta => Boolean(tab));
    const missing = tabMetas.filter((tab) => !tabOrderIds.includes(tab.id));
    return [...ordered, ...missing].map((tab) => ({
      ...tab,
      active: tab.id === visibleTabId,
    }));
  }, [tabMetas, tabOrderIds, visibleTabId]);

  useEffect(() => {
    const ids = tabMetas.map((tab) => tab.id);
    setTabOrderIds((current) => {
      const next = current.filter((id) => ids.includes(id));
      for (const id of ids) {
        if (!next.includes(id)) next.push(id);
      }
      return next.join("\u0000") === current.join("\u0000") ? current : next;
    });
  }, [tabMetas]);

  useEffect(() => {
    if (!renamingTopicId || activeTab?.topicId === renamingTopicId) return;
    topicRenameSkipCommitRef.current = false;
    topicRenameCommitHandledRef.current = false;
    setRenamingTopicId(null);
    setTopicTitleDraft("");
  }, [activeTab?.topicId, renamingTopicId]);

  const syncModeToController = useCallback((m: Mode) => setControllerMode(m), [setControllerMode]);

  // applyMode is the single source of truth for the input mode: it updates the
  // local pill and pushes the matching gate state to the controller (plan = read
  // only; yolo = auto-approve every tool call). normal clears both.
  const applyMode = useCallback(
    (m: Mode) => {
      setMode(m);
      void syncModeToController(m);
    },
    [setMode, syncModeToController],
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
  const todoEntry = useMemo(() => {
    for (let i = state.items.length - 1; i >= 0; i--) {
      const it = state.items[i];
      if (it.kind === "tool" && it.name === "todo_write" && !it.parentId && it.status === "done" && !it.error) {
        return { item: it, index: i };
      }
    }
    return null;
  }, [state.items]);
  const todoItem = todoEntry?.item ?? null;
  const todos = useMemo(() => (todoItem ? parseTodos(todoItem.args) : []), [todoItem]);
  const [dismissedTodo, setDismissedTodo] = useState<string | null>(null);
  const showTodos =
    !!todoItem &&
    todoItem.id !== dismissedTodo &&
    todos.length > 0 &&
    todos.some((t) => t.status !== "completed");
  const [todoNow, setTodoNow] = useState(() => Date.now());
  const todoSeenRef = useRef<{ id: string; at: number } | null>(null);

  useEffect(() => {
    if (!todoItem) {
      todoSeenRef.current = null;
      return;
    }
    if (todoSeenRef.current?.id !== todoItem.id) {
      todoSeenRef.current = { id: todoItem.id, at: Date.now() };
      setTodoNow(Date.now());
    }
  }, [todoItem]);

  useEffect(() => {
    if (!showTodos) return;
    const id = window.setInterval(() => setTodoNow(Date.now()), 15000);
    return () => window.clearInterval(id);
  }, [showTodos]);

  const todoStale = useMemo(() => {
    if (!showTodos || !todoEntry) return false;
    const after = state.items.slice(todoEntry.index + 1);
    const completedToolsAfter = after.filter(
      (it) => it.kind === "tool" && it.name !== "todo_write" && !it.parentId && (it.status === "done" || it.status === "error"),
    ).length;
    const finalAssistantAfter = after.some((it) => it.kind === "assistant" && !it.streaming && it.text.trim() !== "");
    const readinessNoticeAfter = after.some(
      (it) => it.kind === "notice" && /final-answer readiness|todo_write|complete_step/i.test(it.text),
    );
    const staleByTime = state.running && todoSeenRef.current?.id === todoEntry.item.id && todoNow - todoSeenRef.current.at > 90_000;
    return completedToolsAfter >= 2 || finalAssistantAfter || readinessNoticeAfter || staleByTime;
  }, [showTodos, state.items, state.running, todoEntry, todoNow]);

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

  const addWorkspaceTextToComposer = useCallback((text: string) => {
    setComposerInsertRequest({ id: Date.now(), text });
  }, []);

  const handleTabChange = useCallback(async (id: string) => {
    await switchTab(id);
    await refreshTabMetas();
    setTabRevealSignal((signal) => signal + 1);
  }, [refreshTabMetas, switchTab]);

  const handleTabClose = useCallback(async (id: string) => {
    setModesByTab((current) => {
      if (!(id in current)) return current;
      const next = { ...current };
      delete next[id];
      return next;
    });
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
    await closeTab(id);
    await refreshTabMetas();
    setTabRevealSignal((signal) => signal + 1);
  }, [activeTabId, closeTab, refreshTabMetas]);

  const handleTabsClose = useCallback(async (ids: string[], nextActiveTabId?: string) => {
    const currentIds = tabMetas.map((tab) => tab.id);
    const targets = ids.filter((id, index) => currentIds.includes(id) && ids.indexOf(id) === index);
    if (targets.length === 0) return;
    for (const id of targets) {
      await closeTab(id);
    }
    if (nextActiveTabId && currentIds.includes(nextActiveTabId)) {
      await switchTab(nextActiveTabId);
    }
    await refreshTabMetas();
    setTabRevealSignal((signal) => signal + 1);
  }, [closeTab, refreshTabMetas, switchTab, tabMetas]);

  const handleTabsReorder = useCallback(async (ids: string[]) => {
    setTabOrderIds(ids);
    setTabMetas((current) => {
      const byId = new Map(current.map((tab) => [tab.id, tab]));
      const ordered = ids.map((id) => byId.get(id)).filter((tab): tab is TabMeta => Boolean(tab));
      return ordered.length === current.length ? ordered : current;
    });
    await reorderTabs(ids);
    await refreshTabMetas();
    setTabRevealSignal((signal) => signal + 1);
  }, [refreshTabMetas, reorderTabs]);

  const handleNewTab = useCallback(async () => {
    const activeWorkspaceRoot = activeTab?.workspaceRoot || state.meta?.cwd || "";
    const targetScope = activeTab?.scope === "global" || !activeWorkspaceRoot ? "global" : "project";
    const workspaceRoot = targetScope === "project" ? activeWorkspaceRoot : "";
    const topic = await app.CreateTopic(targetScope, workspaceRoot, "");
    if (targetScope === "global" || !workspaceRoot) {
      await openGlobalTab(topic.id);
    } else {
      await openProjectTab(workspaceRoot, topic.id);
    }
    setProjectRevision((value) => value + 1);
    await refreshTabMetas();
    setTabRevealSignal((signal) => signal + 1);
  }, [activeTab?.scope, activeTab?.workspaceRoot, openGlobalTab, openProjectTab, refreshTabMetas, state.meta?.cwd]);

  const handleOpenTopic = useCallback(async (scope: string, workspaceRoot: string, topicId: string) => {
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
  const onPurgeAllTrashedSessions = useCallback(
    async (paths: string[]) => {
      const uniquePaths = Array.from(new Set(paths));
      for (const path of uniquePaths) {
        await purgeTrashedSession(path);
      }
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

  const renameTopic = useCallback(async (topicId: string, title: string) => {
    const nextTitle = title.trim();
    if (!topicId || !nextTitle) return;
    await app.RenameTopic(topicId, nextTitle);
    await refreshProjectsAndTabs();
  }, [refreshProjectsAndTabs]);

  const startActiveTopicRename = useCallback(() => {
    if (!activeTab?.topicId) return;
    topicRenameSkipCommitRef.current = false;
    topicRenameCommitHandledRef.current = false;
    setRenamingTopicId(activeTab.topicId);
    setTopicTitleDraft(activeTab.topicTitle || "");
  }, [activeTab?.topicId, activeTab?.topicTitle]);

  const cancelActiveTopicRename = useCallback(() => {
    topicRenameSkipCommitRef.current = true;
    topicRenameCommitHandledRef.current = true;
    setRenamingTopicId(null);
    setTopicTitleDraft("");
  }, []);

  const commitActiveTopicRename = useCallback(async () => {
    if (topicRenameSkipCommitRef.current) {
      topicRenameSkipCommitRef.current = false;
      topicRenameCommitHandledRef.current = false;
      setRenamingTopicId(null);
      return;
    }
    if (topicRenameCommitHandledRef.current) return;
    topicRenameCommitHandledRef.current = true;
    const topicId = renamingTopicId;
    setRenamingTopicId(null);
    if (!topicId) return;
    const nextTitle = topicTitleDraft.trim();
    if (!nextTitle) return;
    await renameTopic(topicId, nextTitle);
  }, [renameTopic, renamingTopicId, topicTitleDraft]);

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
  const sidebarNavTooltipDisabled = !sidebarCollapsed;
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
              onTopicsChanged={refreshProjectsAndTabs}
              onRenameTopic={renameTopic}
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
            <Tooltip label={t("sidebar.allHistory")} fill side="right" disabled={sidebarNavTooltipDisabled}>
              <button
                className="sidebar__navitem"
                onClick={() => void openAllHistory()}
              >
                <History size={15} />
                <span>{t("sidebar.allHistory")}</span>
              </button>
            </Tooltip>
            <Tooltip label={t("sidebar.trash")} fill side="right" disabled={sidebarNavTooltipDisabled}>
              <button
                className="sidebar__navitem"
                onClick={() => void openTrash()}
              >
                <Trash2 size={15} />
                <span>{t("sidebar.trash")}</span>
              </button>
            </Tooltip>
            <Tooltip label={t("topbar.memory")} fill side="right" disabled={sidebarNavTooltipDisabled}>
              <button className="sidebar__navitem" onClick={() => void openMemory()}>
                <Brain size={15} />
                <span>{t("topbar.memory")}</span>
              </button>
            </Tooltip>
            <Tooltip label={t("caps.title")} fill side="right" disabled={sidebarNavTooltipDisabled}>
              <button className="sidebar__navitem" onClick={() => setCapsOpen(true)}>
                <Blocks size={15} />
                <span>{t("caps.title")}</span>
              </button>
            </Tooltip>
            <Tooltip label={t("topbar.settings")} fill side="right" disabled={sidebarNavTooltipDisabled}>
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
              onTabsClose={(ids, nextActiveTabId) => void handleTabsClose(ids, nextActiveTabId)}
              onTabsReorder={(ids) => void handleTabsReorder(ids)}
              onNewTab={() => void handleNewTab()}
            />
          </header>

          <>
          <header className="topicbar">
            <div className="topicbar__identity">
              <div className="topicbar__title-row">
                {topicbarEditing ? (
                  <div className="topicbar__title-edit">
                    {topicbarProjectPrefix && (
                      <span className="topicbar__title-prefix">{topicbarProjectPrefix} /</span>
                    )}
                    <input
                      autoFocus
                      className="topicbar__title-input"
                      value={topicTitleDraft}
                      onChange={(event) => setTopicTitleDraft(event.target.value)}
                      onKeyDown={(event: KeyboardEvent<HTMLInputElement>) => {
                        if (event.key === "Enter") {
                          event.preventDefault();
                          void commitActiveTopicRename();
                        }
                        if (event.key === "Escape") {
                          event.preventDefault();
                          cancelActiveTopicRename();
                        }
                      }}
                      onBlur={() => void commitActiveTopicRename()}
                    />
                  </div>
                ) : (
                  <h1>{topicTitle(activeTab)}</h1>
                )}
                <Tooltip label={t("topicBar.renameSession")}>
                  <button
                    className="topicbar__icon-btn"
                    type="button"
                    disabled={!activeTab?.topicId || topicbarEditing}
                    onClick={startActiveTopicRename}
                    aria-label={t("topicBar.renameSession")}
                  >
                    <Pencil size={14} />
                  </button>
                </Tooltip>
              </div>
            </div>
            <div className="topicbar__spacer" />
            <div className="topicbar__actions">
              <Tooltip label={t("topicBar.more")}>
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
              <Transcript
                items={state.items}
                live={state.live}
                footerHeight={footerHeight}
                onPrompt={send}
                onRewind={rewind}
                rewindDisabled={state.running || state.approval != null || state.ask != null}
              />
            )}
          </main>

          <footer className="footer" ref={footerRef}>
            {showTodos && <TodoPanel todos={todos} stale={todoStale} onDismiss={() => setDismissedTodo(todoItem!.id)} />}
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
              tabId={activeTabId}
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
              decisionPending={state.approval != null || state.ask != null}
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
              cost={state.sessionCost}
              currency={state.sessionCurrency}
            />
          </footer>
          </>
        </section>

        {workspacePanelOpen && !workspacePanelMaximized && (
          <button
            className="workspace-panel-resizer"
            type="button"
            role="separator"
            aria-orientation="vertical"
            aria-label={t("rightDock.resize")}
            aria-valuemin={RIGHT_DOCK_MIN_WIDTH}
            aria-valuemax={Math.max(RIGHT_DOCK_MAX_WIDTH, workspacePanelRenderWidth)}
            aria-valuenow={workspacePanelRenderWidth}
            onPointerDown={startWorkspacePanelResize}
            onKeyDown={resizeWorkspacePanelWithKeyboard}
            onDoubleClick={() => setSavedWorkspacePanelWidth(workspacePanelResetWidth)}
          />
        )}

        {!workspacePanelOpen && !workspacePanelMaximized && (
          <Tooltip label={t("rightDock.expand")} className="workspace-dock-peek">
            <button
              className="workspace-iconbtn workspace-dock-peek__button"
              type="button"
              onClick={() => openWorkspacePanel("files")}
              aria-label={t("rightDock.expand")}
              aria-pressed={false}
            >
              <PanelRightOpen size={14} />
            </button>
          </Tooltip>
        )}

        {workspacePanelOpen && (
          <aside
            className={[
              "workbench-dock",
              `workbench-dock--${rightDockMode}`,
            ].join(" ")}
            ref={workbenchDockRef}
            aria-label={t("rightDock.workbench")}
          >
            <div className="workbench-dock__tools">
              <div className="workbench-dock__tabs" role="tablist" aria-label={t("rightDock.views")}>
                {SHOW_CONTEXT_DOCK && (
                  <button
                    type="button"
                    role="tab"
                    aria-selected={rightDockMode === "context"}
                    className={`workbench-dock__tab${rightDockMode === "context" ? " workbench-dock__tab--active" : ""}`}
                    onClick={() => openRightDockMode("context")}
                  >
                    <CircleGauge size={13} />
                    {t("rightDock.overview")}
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
              <Tooltip label={t("rightDock.collapse")}>
                <button
                  className="workspace-iconbtn"
                  type="button"
                  aria-label={t("rightDock.collapse")}
                  onClick={closeWorkspacePanel}
                >
                  <PanelRightClose size={14} />
                </button>
              </Tooltip>
            </div>
            <div className="workbench-dock__body">
              {rightDockMode === "context" ? (
                <ContextPanel
                  tabId={activeTabId}
                  context={state.context}
                  usage={state.usage}
                  sessionCost={state.sessionCost}
                  sessionCurrency={state.sessionCurrency}
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
          onPurgeAll={onPurgeAllTrashedSessions}
          onClose={closeHistory}
        />
      )}

      {settingsOpen && <SettingsPanel onClose={() => setSettingsOpen(false)} onChanged={() => void refreshMeta()} />}

      {capsOpen && <CapabilitiesPanel onClose={() => setCapsOpen(false)} />}

      {needsOnboarding && <OnboardingOverlay onComplete={() => setNeedsOnboarding(false)} />}
    </div>
  );
}
