import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { app } from "./lib/bridge";
import { useController } from "./lib/useController";
import type { MemoryView, Mode, SessionMeta, TabInfo } from "./lib/types";
import { parseTodos } from "./lib/tools";
import { Transcript } from "./components/Transcript";
import { ApprovalModal } from "./components/ApprovalModal";
import { AskCard } from "./components/AskCard";
import { TodoPanel } from "./components/TodoPanel";
import { MemoryPanel } from "./components/MemoryPanel";
import { HistoryPanel } from "./components/HistoryPanel";
import { SettingsPanel } from "./components/SettingsPanel";
import { UpdateBanner } from "./components/UpdateBanner";
import { Composer, type EditMode, type ReasoningEffort, type SlashCmd } from "./v1/ui/composer";
import { ContextPanel, type ContextPanelTab } from "./v1/ui/context-panel";
import { Sidebar } from "./v1/ui/sidebar";
import { StatusBar } from "./v1/ui/statusbar";
import { WorkdirPop } from "./v1/ui/workdir-pop";
import { I } from "./v1/icons";
import { t, useLang } from "./v1/i18n";
import {
  FONT_FAMILY,
  FONT_FAMILY_STACK,
  FONT_SCALE,
  FONT_SCALE_ZOOM,
  THEME,
  defaultStyleForTheme,
  isFontFamily,
  isFontScale,
  isTheme,
  isThemeStyle,
  themeForStyle,
  type FontFamily,
  type FontScale,
  type Theme,
  type ThemeStyle,
} from "./v1/theme";
import type { Balance, SessionInfo, Settings, UsageStats } from "./v1/compat-types";
import type { ExternalSessionApp, ImportedMcpServer, JobInfo, McpSpecInfo, SettingsPatch } from "./v1/protocol";
import type { PageId as SettingsPageId } from "./v1/ui/settings";
import type { Item } from "./lib/useController";

function basename(path?: string): string {
  if (!path) return "workspace";
  const parts = path.split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] || path;
}

function toSessionInfo(s: SessionMeta): SessionInfo {
  return {
    name: s.path,
    messageCount: s.turns,
    mtime: new Date(s.modTime).toISOString(),
    summary: s.title?.trim() || s.preview?.trim() || undefined,
  };
}

function toUsageStats(state: ReturnType<typeof useController>["state"]): UsageStats {
  const usage = state.usage;
  return {
    totalCostUsd: usage?.costUsd ?? 0,
    totalPromptTokens: usage?.promptTokens ?? 0,
    totalCompletionTokens: usage?.completionTokens ?? 0,
    cacheHitTokens: usage?.sessionCacheHitTokens ?? usage?.cacheHitTokens ?? 0,
    cacheMissTokens: usage?.sessionCacheMissTokens ?? usage?.cacheMissTokens ?? 0,
    lastCallCacheHit: usage?.cacheHitTokens ?? null,
    lastCallCacheMiss: usage?.cacheMissTokens ?? null,
    reservedTokens: 0,
    liveLogTokens: state.context.used,
  };
}

function toSettings(
  state: ReturnType<typeof useController>["state"],
  editMode: EditMode,
  effort: ReasoningEffort,
): Settings {
  return {
    reasoningEffort: effort,
    editMode,
    budgetUsd: null,
    baseUrl: "https://api.deepseek.com",
    workspaceDir: state.meta?.cwd ?? "",
    recentWorkspaces: state.meta?.cwd ? [state.meta.cwd] : [],
    model: state.meta?.label ?? "deepseek-v4-flash",
    version: "v2-wails",
  };
}

function parseBalanceDisplay(display?: string): { currency: string; total: number } {
  if (!display) return { currency: "CNY", total: 0 };
  const currency = display.includes("$") ? "USD" : "CNY";
  const n = Number(display.replace(/[^\d.-]/g, ""));
  return { currency, total: Number.isFinite(n) ? n : 0 };
}

function toBalance(state: ReturnType<typeof useController>["state"]): Balance | null {
  const source = state.balance;
  if (!source) return null;
  const parsed = parseBalanceDisplay(source.display);
  return {
    currency: parsed.currency,
    total: parsed.total,
    isAvailable: source.available,
    infos: source.available ? [{ currency: parsed.currency, total: parsed.total }] : [],
  };
}

function toJobs(jobs: ReturnType<typeof useController>["state"]["jobs"]): JobInfo[] {
  return jobs.map((job, index) => ({
    id: index + 1,
    tabId: "main",
    sessionLabel: job.kind,
    command: job.label,
    pid: null,
    running: job.status === "running",
    exitCode: null,
    startedAt: job.startedAt,
    outputTail: "",
  }));
}

function joinMentionPath(dir: string, name: string, isDir: boolean): string {
  const normalized = dir.replace(/\\/g, "/");
  const prefix = normalized && !normalized.endsWith("/") ? `${normalized}/` : normalized;
  return `${prefix}${name}${isDir ? "/" : ""}`;
}

function cycleMode(mode: EditMode): EditMode {
  if (mode === "review") return "plan";
  if (mode === "plan") return "yolo";
  return "review";
}

function v2ModeToEditMode(mode: Mode, bypass?: boolean): EditMode {
  if (bypass || mode === "yolo") return "yolo";
  if (mode === "plan") return "plan";
  return "review";
}

function TitleBar({
  model,
  sideOn,
  ctxOn,
  onToggleSide,
  onToggleCtx,
  onOpenSettings,
}: {
  model?: string;
  sideOn: boolean;
  ctxOn: boolean;
  onToggleSide: () => void;
  onToggleCtx: () => void;
  onOpenSettings: () => void;
}) {
  useLang();
  const modelLabel = model?.trim();
  return (
    <header className="titlebar">
      <div className="tb-left">
        <button type="button" className="iconbtn" data-on={sideOn} title={t("app.titlebar.sidebar")} onClick={onToggleSide}>
          <I.panel_l size={14} />
        </button>
        <div className="tb-meta">
          <div className="brand">
            <span className="mark" />
            <span className="brand-name">Reasonix</span>
          </div>
          {modelLabel ? (
            <div className="crumbs">
              <span className="sep">/</span>
              <span className="cur">{modelLabel}</span>
            </div>
          ) : null}
        </div>
      </div>
      <span className="grow" />
      <div className="tb-right">
        <button type="button" className="iconbtn" data-on={ctxOn} title={t("app.titlebar.contextPanel")} onClick={onToggleCtx}>
          <I.panel_r size={14} />
        </button>
        <button type="button" className="iconbtn" title={t("app.titlebar.settings")} onClick={onOpenSettings}>
          <I.more size={14} />
        </button>
      </div>
    </header>
  );
}

function TabBar({
  tabs,
  activeId,
  busy,
  onActivate,
  onClose,
  onNew,
}: {
  tabs: TabInfo[];
  activeId?: string;
  busy: boolean;
  onActivate: (id: string) => void;
  onClose: (id: string) => void;
  onNew: () => void;
}) {
  useLang();
  return (
    <div className="tabbar">
      {tabs.map((tab) => {
        const active = tab.id === activeId;
        const label = basename(tab.workspaceDir);
        return (
          <div
            key={tab.id}
            className="tab"
            data-active={active}
            title={tab.workspaceDir || label}
            onClick={() => onActivate(tab.id)}
          >
            <span className="dot" data-state={active && busy ? "running" : "idle"} />
            <span className="label">{label}</span>
            {tabs.length > 1 ? (
              <span
                className="close"
                onClick={(event) => {
                  event.stopPropagation();
                  onClose(tab.id);
                }}
              >
                <I.x size={11} />
              </span>
            ) : null}
          </div>
        );
      })}
      <div className="tab newtab" title={t("app.tab.newTabTitle")} onClick={onNew}>
        <I.plus size={12} />
        <span style={{ fontSize: 11, marginLeft: 4 }}>{t("app.tab.newTab")}</span>
      </div>
    </div>
  );
}

function conversationMarkdown(items: Item[]): string {
  return items
    .map((item) => {
      if (item.kind === "user") return `## 你\n\n${item.text}`;
      if (item.kind === "assistant") return `## Reasonix\n\n${item.text}`;
      if (item.kind === "tool") {
        const body = item.output || item.error || "";
        return `> 工具 · \`${item.name}\`\n\n${body}`;
      }
      if (item.kind === "notice" || item.kind === "phase") return `> ${item.text}`;
      return "";
    })
    .filter(Boolean)
    .join("\n\n");
}

function sessionTitle(workspaceDir: string | undefined, hasMessages: boolean): string {
  const workspace = workspaceDir ? basename(workspaceDir) : "workspace";
  return hasMessages ? workspace : `${workspace} · 新会话`;
}

function MainHead({
  title,
  model,
  workspaceDir,
  busy,
  hasMessages,
  onAbort,
  onNewChat,
  onCopy,
  onExport,
  onOpenWorkdir,
}: {
  title: string;
  model?: string;
  workspaceDir?: string;
  busy: boolean;
  hasMessages: boolean;
  onAbort: () => void;
  onNewChat: () => void;
  onCopy: () => void;
  onExport: () => void;
  onOpenWorkdir: (anchor: { top?: number; bottom?: number; left: number }) => void;
}) {
  useLang();
  const wsLabel = workspaceDir ? basename(workspaceDir) : t("app.header.noWorkspace");
  return (
    <div className="main-head">
      <div className="title-wrap">
        <h1>
          <span className="editable">{title}</span>
          {busy ? (
            <span className="pill" style={{ color: "var(--accent)" }}>
              <span className="dot" />
              <span className="shimmer">{t("app.header.running")}</span>
            </span>
          ) : null}
        </h1>
        <div className="sub">
          <span
            className="ws-crumb"
            onClick={(event) => {
              const rect = event.currentTarget.getBoundingClientRect();
              onOpenWorkdir({ top: rect.bottom + 6, left: rect.left });
            }}
            style={{ cursor: "pointer" }}
            title={workspaceDir || t("app.header.clickToSelect")}
          >
            <I.folder size={10} /> {wsLabel}
          </span>
          {model ? (
            <span className="pill">
              <I.brain size={10} /> {model}
            </span>
          ) : null}
        </div>
      </div>
      <span className="grow" />
      <button type="button" className="h-btn" onClick={onCopy} disabled={!hasMessages} title={t("app.header.copyMd")}>
        <I.copy size={12} /> {t("app.header.copy")}
      </button>
      <button type="button" className="h-btn" onClick={onExport} disabled={!hasMessages} title={t("app.header.exportMd")}>
        <I.download size={12} /> {t("app.header.export")}
      </button>
      <button type="button" className="h-btn" onClick={onNewChat}>
        <I.plus size={12} /> {t("app.header.newChat")}
      </button>
      {busy ? (
        <button type="button" className="h-btn primary" onClick={onAbort}>
          <I.stop size={12} /> {t("app.header.abort")}
        </button>
      ) : null}
    </div>
  );
}

function EmptyState({
  onPick,
  workspaceDir,
}: {
  onPick: (text: string) => void;
  workspaceDir?: string;
}) {
  useLang();
  const suggestions = [
    t("app.empty.suggestion0"),
    t("app.empty.suggestion1"),
    t("app.empty.suggestion2"),
    t("app.empty.suggestion3"),
    "/help",
  ];
  const wsLabel = workspaceDir ? basename(workspaceDir) : null;
  return (
    <div
      style={{
        padding: "48px 16px 24px",
        textAlign: "center",
        color: "var(--muted)",
        fontFamily: "var(--font-sans, 'Geist', sans-serif)",
      }}
    >
      <div
        style={{
          width: 56,
          height: 56,
          borderRadius: 12,
          margin: "0 auto 14px",
          background: "linear-gradient(135deg, var(--accent), var(--violet))",
          position: "relative",
        }}
      >
        <span
          style={{
            position: "absolute",
            inset: 8,
            borderRadius: 6,
            background: "var(--bg)",
          }}
        />
      </div>
      <div style={{ fontSize: 18, fontWeight: 600, color: "var(--fg)", marginBottom: 4 }}>
        {t("app.empty.welcome")}
      </div>
      <div style={{ fontSize: 12, marginBottom: 18 }}>
        {wsLabel ? (
          <>
            {t("app.empty.currentWorkspace")}
            <code style={{ fontFamily: "Geist Mono, monospace" }}>{wsLabel}</code>
          </>
        ) : (
          t("app.empty.selectWorkspace")
        )}
      </div>
      <div
        style={{
          display: "flex",
          flexWrap: "wrap",
          gap: 8,
          justifyContent: "center",
          maxWidth: 540,
          margin: "0 auto",
        }}
      >
        {suggestions.map((suggestion) => (
          <button key={suggestion} type="button" className="btn" style={{ fontSize: 11.5 }} onClick={() => onPick(suggestion)}>
            {suggestion}
          </button>
        ))}
      </div>
    </div>
  );
}

function NeedsSetupView({
  workspaceDir,
  onPickWorkspace,
  onSubmit,
}: {
  workspaceDir?: string;
  onPickWorkspace: () => void;
  onSubmit: (key: string) => void;
}) {
  useLang();
  const [key, setKey] = useState("");
  return (
    <div
      style={{
        flex: 1,
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        padding: 24,
        gap: 18,
      }}
    >
      <div style={{ fontSize: 18, fontWeight: 600 }}>{t("app.setup.welcome")}</div>
      <div style={{ fontSize: 12.5, color: "var(--muted)", maxWidth: 400, textAlign: "center" }}>
        {t("app.setup.description")}
      </div>
      <div style={{ width: "min(420px, 100%)", display: "flex", flexDirection: "column", gap: 10 }}>
        <div className="setting-row" style={{ borderBottom: "none" }}>
          <div className="l">
            <div className="n">{t("app.setup.workspace")}</div>
            <div className="h">{workspaceDir || t("app.setup.notSelected")}</div>
          </div>
          <button type="button" className="btn" onClick={onPickWorkspace}>
            {t("app.setup.choose")}
          </button>
        </div>
        <input
          className="field mono"
          type="password"
          value={key}
          onChange={(event) => setKey(event.target.value)}
          placeholder="sk-..."
          style={{ width: "100%" }}
        />
        <button type="button" className="btn primary" disabled={!key.trim()} onClick={() => onSubmit(key.trim())}>
          {t("app.setup.saveAndStart")}
        </button>
      </div>
    </div>
  );
}

function OpeningTabView() {
  return (
    <div
      style={{
        flex: 1,
        display: "grid",
        placeItems: "center",
        padding: 24,
        color: "var(--muted)",
        fontSize: 13,
      }}
    >
      正在打开新标签...
    </div>
  );
}

export default function App() {
  const controller = useController();
  const {
    state,
    send,
    cancel,
    approve,
    answerQuestion,
    setPlan,
    setBypass,
    newSession,
    activateTab,
    listSessions,
    resumeSession,
    deleteSession,
    renameSession,
    refreshMeta,
    pickWorkspace,
    compact,
    rewind,
    setModel,
    fetchMemory,
    remember,
    saveDoc,
  } = controller;

  useLang();

  const [draft, setDraft] = useState("");
  const [sessions, setSessions] = useState<SessionMeta[]>([]);
  const [sideOpen, setSideOpen] = useState(true);
  const [ctxOpen, setCtxOpen] = useState(false);
  const [contextPanelTab, setContextPanelTab] = useState<ContextPanelTab>("files");
  const [contextPanelTabNonce, setContextPanelTabNonce] = useState(0);
  const [wdOpen, setWdOpen] = useState(false);
  const [wdAnchor, setWdAnchor] = useState<{ top?: number; bottom?: number; left: number } | undefined>();
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsPage, setSettingsPage] = useState<SettingsPageId>("general");
  const [mcpSpecs, setMcpSpecs] = useState<McpSpecInfo[]>([]);
  const [mcpBridged, setMcpBridged] = useState(false);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [memoryOpen, setMemoryOpen] = useState(false);
  const [memoryView, setMemoryView] = useState<MemoryView | null>(null);
  const [currency, setCurrency] = useState<"CNY" | "USD">("CNY");
  const [theme, setTheme] = useState<Theme>(() => {
    const style = localStorage.getItem("reasonix.themeStyle");
    if (isThemeStyle(style)) return themeForStyle(style);
    const storedTheme = localStorage.getItem("reasonix.theme");
    return isTheme(storedTheme) ? storedTheme : THEME.DARK;
  });
  const [themeStyle, setThemeStyle] = useState<ThemeStyle>(() => {
    const style = localStorage.getItem("reasonix.themeStyle");
    if (isThemeStyle(style)) return style;
    const storedTheme = localStorage.getItem("reasonix.theme");
    return defaultStyleForTheme(isTheme(storedTheme) ? storedTheme : THEME.DARK);
  });
  const [fontScale, setFontScale] = useState<FontScale>(() => {
    const value = localStorage.getItem("reasonix.fontScale");
    return isFontScale(value) ? value : FONT_SCALE.MEDIUM;
  });
  const [fontFamily, setFontFamily] = useState<FontFamily>(() => {
    const value = localStorage.getItem("reasonix.fontFamily");
    return isFontFamily(value) ? value : FONT_FAMILY.SANS;
  });
  const [customFontFamily, setCustomFontFamily] = useState<string>(() => localStorage.getItem("reasonix.customFontFamily") ?? "");
  const [effort, setEffort] = useState<ReasoningEffort>("high");
  const [editMode, setEditMode] = useState<EditMode>(() => v2ModeToEditMode("normal"));
  const [mentionResults, setMentionResults] = useState<{ nonce: number; query: string; results: string[] } | null>(null);
  const [tabs, setTabs] = useState<TabInfo[]>([]);
  const composerRef = useRef<HTMLTextAreaElement>(null);

  const ready = state.meta?.ready ?? false;
  const workspaceDir = state.meta?.cwd ?? "";
  const modelLabel = state.meta?.label ?? "deepseek-v4-flash";
  const activeTabId = state.meta?.tabId ?? tabs.find((tab) => tab.active)?.id;

  const refreshTabs = useCallback(async () => {
    const next = await app.Tabs().catch(() => [] as TabInfo[]);
    setTabs(next);
    return next;
  }, []);

  useEffect(() => {
    void refreshTabs();
  }, [refreshTabs]);

  useEffect(() => {
    if (!state.meta?.tabId) return;
    setTabs((current) =>
      current.map((tab) =>
        tab.id === state.meta?.tabId ? { ...tab, workspaceDir: state.meta?.cwd ?? tab.workspaceDir, active: true } : { ...tab, active: false },
      ),
    );
  }, [state.meta?.cwd, state.meta?.tabId]);

  const reloadSessions = useCallback(async () => {
    const next = await listSessions();
    setSessions(next);
  }, [listSessions]);

  useEffect(() => {
    document.documentElement.dataset.platform = "macos";
    document.documentElement.dataset.theme = theme;
    document.documentElement.dataset.themeStyle = themeStyle;
    localStorage.setItem("reasonix.theme", theme);
    localStorage.setItem("reasonix.themeStyle", themeStyle);
  }, [theme, themeStyle]);

  const setThemeMode = useCallback((nextTheme: Theme) => {
    setTheme(nextTheme);
    setThemeStyle((currentStyle) =>
      themeForStyle(currentStyle) === nextTheme ? currentStyle : defaultStyleForTheme(nextTheme),
    );
  }, []);

  useEffect(() => {
    document.documentElement.style.setProperty("zoom", String(FONT_SCALE_ZOOM[fontScale]));
    localStorage.setItem("reasonix.fontScale", fontScale);
  }, [fontScale]);

  useEffect(() => {
    const custom = customFontFamily.trim();
    const stack =
      fontFamily === FONT_FAMILY.CUSTOM && custom
        ? custom
        : FONT_FAMILY_STACK[fontFamily] ?? FONT_FAMILY_STACK.sans;
    document.documentElement.style.setProperty("--font-sans", stack);
    localStorage.setItem("reasonix.fontFamily", fontFamily);
    localStorage.setItem("reasonix.customFontFamily", customFontFamily);
  }, [fontFamily, customFontFamily]);

  useEffect(() => {
    void reloadSessions();
  }, [reloadSessions, ready]);

  useEffect(() => {
    setEditMode((current) => {
      const next = v2ModeToEditMode(state.meta?.bypass ? "yolo" : "normal", state.meta?.bypass);
      return current === "plan" ? current : next;
    });
  }, [state.meta?.bypass]);

  const applyEditMode = useCallback(
    (mode: EditMode) => {
      setEditMode(mode);
      setPlan(mode === "plan");
      setBypass(mode === "yolo");
    },
    [setBypass, setPlan],
  );

  const onSaveSettings = useCallback(
    (patch: SettingsPatch) => {
      if (patch.reasoningEffort) setEffort(patch.reasoningEffort);
      if (patch.editMode) applyEditMode(patch.editMode);
      if (patch.model) void setModel(patch.model);
    },
    [applyEditMode, setModel],
  );

  const onSaveApiKey = useCallback((key: string) => {
    void app.Settings()
      .then((view) => {
        const providerRef = view.defaultModel.includes("/")
          ? view.defaultModel.split("/")[0]
          : view.defaultModel;
        const provider = view.providers.find((p) => p.name === providerRef) ?? view.providers[0];
        if (!provider?.apiKeyEnv) return;
        return app.SetProviderKey(provider.apiKeyEnv, key);
      })
      .then(refreshMeta)
      .catch(() => undefined);
  }, [refreshMeta]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Tab" && event.shiftKey) {
        event.preventDefault();
        applyEditMode(cycleMode(editMode));
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [applyEditMode, editMode]);

  const v1Sessions = useMemo(() => sessions.map(toSessionInfo), [sessions]);
  const activeSession = sessions.find((session) => session.current)?.path ?? sessions[0]?.path;
  const settings = useMemo(() => toSettings(state, editMode, effort), [state, editMode, effort]);
  const usage = useMemo(() => toUsageStats(state), [state]);
  const balance = useMemo(() => toBalance(state), [state]);
  const jobs = useMemo(() => toJobs(state.jobs), [state.jobs]);
  const hasMessages = state.items.some((item) => item.kind === "user" || item.kind === "assistant");
  const currentSessionTitle = sessionTitle(workspaceDir, hasMessages);
  const todos = useMemo(() => {
    for (let i = state.items.length - 1; i >= 0; i--) {
      const item = state.items[i];
      if (item.kind === "tool" && item.name === "todo_write") return parseTodos(item.args);
    }
    return [];
  }, [state.items]);

  const openWorkdir = useCallback((anchor: { top?: number; bottom?: number; left: number }) => {
    setWdAnchor(anchor);
    setWdOpen(true);
  }, []);

  const openContextPanel = useCallback((tab: ContextPanelTab = "files") => {
    setContextPanelTab(tab);
    setContextPanelTabNonce((n) => n + 1);
    setCtxOpen(true);
  }, []);

  const openSettingsPage = useCallback((page: SettingsPageId = "general") => {
    setSettingsPage(page);
    setSettingsOpen(true);
  }, []);

  const onPickWorkspace = useCallback(async () => {
    await pickWorkspace();
    await refreshMeta();
    await reloadSessions();
  }, [pickWorkspace, refreshMeta, reloadSessions]);

  const onResumeSession = useCallback(
    async (path: string) => {
      await resumeSession(path);
      await reloadSessions();
    },
    [reloadSessions, resumeSession],
  );

  const onDeleteSession = useCallback(
    async (path: string) => {
      await deleteSession(path);
      await reloadSessions();
    },
    [deleteSession, reloadSessions],
  );

  const onRenameSession = useCallback(
    async (path: string, title: string) => {
      await renameSession(path, title);
      await reloadSessions();
    },
    [reloadSessions, renameSession],
  );

  const onNewSession = useCallback(async () => {
    await newSession();
    await reloadSessions();
    requestAnimationFrame(() => composerRef.current?.focus());
  }, [newSession, reloadSessions]);

  const onOpenTab = useCallback(async () => {
    const tab = await app.OpenTab().catch(() => null);
    if (!tab) return;
    setTabs((current) => [...current.map((item) => ({ ...item, active: false })), tab]);
    await activateTab(tab.id);
    await reloadSessions();
    requestAnimationFrame(() => composerRef.current?.focus());
  }, [activateTab, reloadSessions]);

  const onActivateTab = useCallback(
    async (id: string) => {
      if (!id || id === activeTabId) return;
      const next = await app.ActivateTab(id).catch(() => [] as TabInfo[]);
      if (next.length) setTabs(next);
      await activateTab(id);
      await reloadSessions();
      requestAnimationFrame(() => composerRef.current?.focus());
    },
    [activateTab, activeTabId, reloadSessions],
  );

  const onCloseTab = useCallback(
    async (id: string) => {
      const next = await app.CloseTab(id).catch(() => [] as TabInfo[]);
      if (!next.length) return;
      setTabs(next);
      const active = next.find((tab) => tab.active);
      if (active && active.id !== activeTabId) {
        await activateTab(active.id);
        await reloadSessions();
      }
    },
    [activateTab, activeTabId, reloadSessions],
  );

  const copyConversation = useCallback(() => {
    const body = conversationMarkdown(state.items);
    if (!body) return;
    void navigator.clipboard.writeText(body);
  }, [state.items]);

  const exportConversation = useCallback(() => {
    const body = conversationMarkdown(state.items);
    if (!body) return;
    const blob = new Blob([body], { type: "text/markdown;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${basename(workspaceDir) || "reasonix"}-conversation.md`;
    a.click();
    URL.revokeObjectURL(url);
  }, [state.items, workspaceDir]);

  const openMemory = useCallback(async () => {
    setMemoryOpen(true);
    setMemoryView(await fetchMemory());
  }, [fetchMemory]);

  const handleSend = useCallback(() => {
    const text = draft.trim();
    if (!text) return;
    if (text === "/memory") {
      setDraft("");
      void openMemory();
      return;
    }
    if (text === "/compact") {
      setDraft("");
      compact();
      return;
    }
    const modelMatch = /^\/model\s+(.+)$/.exec(text);
    if (modelMatch?.[1]) {
      setDraft("");
      void setModel(modelMatch[1].trim());
      return;
    }
    if (/^\/mcp(?:\s+status)?\s*$/.test(text)) {
      setDraft("");
      openContextPanel("tools");
      return;
    }
    setDraft("");
    send(text);
    void reloadSessions();
  }, [compact, draft, openContextPanel, openMemory, reloadSessions, send, setModel]);

  const handleAbort = useCallback(() => {
    const restored = cancel();
    if (restored) setDraft(restored);
  }, [cancel]);

  const slashCommands = useMemo<SlashCmd[]>(
    () => [
      { cmd: "/help", desc: "查看所有命令", insertOnly: true, run: () => setDraft("/help ") },
      { cmd: "/memory", desc: "打开记忆面板", run: () => void openMemory() },
      { cmd: "/compact", desc: "压缩当前上下文", run: () => compact() },
      { cmd: "/model", desc: "切换模型", insertOnly: true, run: () => setDraft("/model ") },
      { cmd: "/mcp", desc: "查看 MCP 状态", run: () => openContextPanel("tools") },
    ],
    [compact, openContextPanel, openMemory],
  );

  const handleMentionQuery = useCallback((query: string, nonce: number) => {
    const slash = query.lastIndexOf("/");
    const dir = slash >= 0 ? query.slice(0, slash + 1) : "";
    const frag = slash >= 0 ? query.slice(slash + 1).toLowerCase() : query.toLowerCase();
    app
      .ListDir(dir)
      .then((entries) => {
        const results = (entries ?? [])
          .filter((entry) => entry.name.toLowerCase().includes(frag))
          .slice(0, 16)
          .map((entry) => joinMentionPath(dir, entry.name, entry.isDir));
        setMentionResults({ nonce, query, results });
      })
      .catch(() => setMentionResults({ nonce, query, results: [] }));
  }, []);

  const submitApiKey = useCallback(
    async (key: string) => {
      await app.SetProviderKey("DEEPSEEK_API_KEY", key).catch(() => {});
      await refreshMeta();
    },
    [refreshMeta],
  );

  const refreshMcpSpecs = useCallback(async () => {
    const result = await app.MCPSpecs().catch(() => ({ specs: [], bridged: false }));
    setMcpSpecs(
      result.specs.map((spec) => ({
        ...spec,
        name: spec.name ?? null,
      })),
    );
    setMcpBridged(result.bridged);
  }, []);

  useEffect(() => {
    void refreshMcpSpecs();
  }, [refreshMcpSpecs]);

  const importCcSwitchMcp = useCallback(async () => {
    await app.ImportCcSwitchMCP();
    await refreshMcpSpecs();
  }, [refreshMcpSpecs]);

  const addMcpSpec = useCallback(
    (spec: string) => {
      void app.AddMCPServer(spec).finally(refreshMcpSpecs);
    },
    [refreshMcpSpecs],
  );

  const removeMcpSpec = useCallback(
    (raw: string) => {
      void app.RemoveMCPServer(raw).finally(refreshMcpSpecs);
    },
    [refreshMcpSpecs],
  );

  const updateMcpSpec = useCallback(
    (raw: string, server: ImportedMcpServer) => {
      void app.UpdateMCPServer(raw, server).finally(refreshMcpSpecs);
    },
    [refreshMcpSpecs],
  );

  const retryMcpSpec = useCallback(
    (raw: string) => {
      void app.RetryMCPServer(raw).finally(refreshMcpSpecs);
    },
    [refreshMcpSpecs],
  );

  const importSources = useMemo<ExternalSessionApp[]>(() => [], []);

  return (
    <div
      className="app rx-v1-shell"
      data-theme={theme}
      data-theme-style={themeStyle}
      data-side-collapsed={!sideOpen}
      data-ctx-collapsed={!ctxOpen}
      style={{ "--side-width": sideOpen ? "244px" : "0px", "--ctx-width": ctxOpen ? "320px" : "0px" } as React.CSSProperties}
    >
      <TitleBar
        model={modelLabel}
        sideOn={sideOpen}
        ctxOn={ctxOpen}
        onToggleSide={() => setSideOpen((open) => !open)}
        onToggleCtx={() => {
          if (ctxOpen) setCtxOpen(false);
          else openContextPanel(contextPanelTab);
        }}
        onOpenSettings={() => openSettingsPage()}
      />
      <TabBar
        tabs={tabs.length ? tabs : [{ id: activeTabId || "main", workspaceDir, active: true }]}
        activeId={activeTabId}
        busy={state.running}
        onActivate={(id) => void onActivateTab(id)}
        onClose={(id) => void onCloseTab(id)}
        onNew={() => void onOpenTab()}
      />

      {sideOpen ? (
        <Sidebar
          sessions={v1Sessions}
          importSources={importSources}
          activeName={activeSession}
          workspaceDir={workspaceDir}
          onNewChat={() => void onNewSession()}
          onLoadSession={(name) => void onResumeSession(name)}
          onDeleteSession={(name) => void onDeleteSession(name)}
          onRenameSession={(name, nextTitle) => void onRenameSession(name, nextTitle)}
          onRefreshImportSources={() => undefined}
          onImportDetectedSessions={() => undefined}
          onImportSession={() => undefined}
          onOpenWorkdir={openWorkdir}
          onOpenSettings={() => openSettingsPage()}
          onOpenCommands={() => setHistoryOpen(true)}
        />
      ) : null}

      <main className="main">
        <UpdateBanner />
        {state.meta?.startupErr ? <div className="banner banner--error">{state.meta.startupErr}</div> : null}
        {state.meta?.opening ? (
          <OpeningTabView />
        ) : !ready ? (
          <NeedsSetupView workspaceDir={workspaceDir} onPickWorkspace={() => void onPickWorkspace()} onSubmit={(key) => void submitApiKey(key)} />
        ) : (
          <>
            <MainHead
              title={currentSessionTitle}
              model={modelLabel}
              workspaceDir={workspaceDir}
              busy={state.running}
              hasMessages={hasMessages}
              onAbort={handleAbort}
              onNewChat={() => void onNewSession()}
              onCopy={copyConversation}
              onExport={exportConversation}
              onOpenWorkdir={openWorkdir}
            />
            <div className="thread">
              {state.items.length === 0 ? (
                <EmptyState
                  workspaceDir={workspaceDir}
                  onPick={(text) => {
                    setDraft(text);
                    requestAnimationFrame(() => composerRef.current?.focus());
                  }}
                />
              ) : (
                <Transcript items={state.items} onPrompt={send} onRewind={rewind} />
              )}
            </div>
            <TodoPanel todos={todos} onDismiss={() => undefined} />
            <Composer
              draft={draft}
              setDraft={setDraft}
              onSend={handleSend}
              onAbort={handleAbort}
              disabled={!ready}
              busy={state.running}
              busyLabel={state.running ? "Reasoning" : undefined}
              modelLabel={modelLabel}
              reasoningEffort={effort}
              onModelChange={(model) => void setModel(model)}
              onEffortChange={setEffort}
              editMode={editMode}
              onEditModeChange={applyEditMode}
              textareaRef={composerRef}
              slashCommands={slashCommands}
              onMentionQuery={handleMentionQuery}
              mentionResults={mentionResults}
              workspaceDir={workspaceDir}
            />
          </>
        )}
      </main>

      {ctxOpen ? (
        <ContextPanel
          settings={settings}
          usage={usage}
          mcpSpecs={mcpSpecs}
          mcpBridged={false}
          sessionFiles={[]}
          memory={[]}
          memoryDetail={null}
          activeTab={contextPanelTab}
          activeTabNonce={contextPanelTabNonce}
          onReadMemory={() => undefined}
          onOpenMcpSettings={() => openSettingsPage("mcp")}
          onEditMcpSpec={() => openSettingsPage("mcp")}
          onRetryMcpSpec={retryMcpSpec}
        />
      ) : null}

      <StatusBar
        settings={settings}
        balance={balance}
        usage={usage}
        busy={state.running}
        ready={ready}
        currency={currency}
        theme={theme}
        themeStyle={themeStyle}
        jobs={jobs}
        jobsOpen={false}
        onToggleJobs={() => undefined}
        onSetThemeStyle={setThemeStyle}
        onToggleCurrency={() => setCurrency((current) => (current === "CNY" ? "USD" : "CNY"))}
        onOpenSettings={() => openSettingsPage()}
        onOpenWorkdir={openWorkdir}
      />

      <WorkdirPop
        open={wdOpen}
        onClose={() => setWdOpen(false)}
        recent={workspaceDir ? [workspaceDir] : []}
        current={workspaceDir}
        anchor={wdAnchor}
        onPick={() => void onPickWorkspace()}
        onBrowse={() => void onPickWorkspace()}
      />

      {state.approval && <ApprovalModal approval={state.approval} onAnswer={(allow, session) => approve(state.approval!.id, allow, session)} />}
      {state.ask && <AskCard ask={state.ask} onAnswer={answerQuestion} onDismiss={() => answerQuestion(state.ask!.id, [])} />}
      {historyOpen && (
        <HistoryPanel
          sessions={sessions}
          onResume={(path) => void onResumeSession(path)}
          onDelete={(path) => void onDeleteSession(path)}
          onRename={(path, nextTitle) => void onRenameSession(path, nextTitle)}
          onClose={() => setHistoryOpen(false)}
        />
      )}
      {memoryOpen && (
        <MemoryPanel
          view={memoryView}
          onClose={() => setMemoryOpen(false)}
          onRemember={async (scope, note) => {
            await remember(scope, note);
            setMemoryView(await fetchMemory());
          }}
          onSaveDoc={async (path, body) => {
            await saveDoc(path, body);
            setMemoryView(await fetchMemory());
          }}
        />
      )}
      {settingsOpen && (
        <SettingsPanel
          settings={settings}
          balance={balance}
          usage={usage}
          currency={currency}
          theme={theme}
          themeStyle={themeStyle}
          onSetTheme={setThemeMode}
          onSetThemeStyle={(style) => {
            setThemeStyle(style);
            setTheme(themeForStyle(style));
          }}
          fontScale={fontScale}
          onSetFontScale={setFontScale}
          fontFamily={fontFamily}
          onSetFontFamily={setFontFamily}
          customFontFamily={customFontFamily}
          onSetCustomFontFamily={setCustomFontFamily}
          initialPage={settingsPage}
          mcpSpecs={mcpSpecs}
          mcpBridged={mcpBridged}
          onImportCcSwitchMcp={importCcSwitchMcp}
          onAddMcpSpec={addMcpSpec}
          onRemoveMcpSpec={removeMcpSpec}
          onUpdateMcpSpec={updateMcpSpec}
          onRetryMcpSpec={retryMcpSpec}
          onClose={() => setSettingsOpen(false)}
          onSave={onSaveSettings}
          onSaveApiKey={onSaveApiKey}
        />
      )}
    </div>
  );
}
