import { useCallback, useEffect, useMemo, useState } from "react";
import {
  MessageSquare,
  Plus,
  Server,
  Sparkles,
} from "lucide-react";
import {
  LocalBackend,
  RemoteBackend,
  type SessionBackend,
} from "./backend/session-backend";
import type { SessionDescriptor, SessionRuntime } from "./protocol/types";
import { resolveLocale, t, type Locale } from "./i18n/messages";
import { applyPlatform, detectPlatform, type Platform } from "./lib/platform";
import { ChatView, type ChatLine } from "./components/ChatView";
import { EmptyState } from "./components/EmptyState";
import { IconButton } from "./components/IconButton";
import { NewSessionSheet } from "./components/NewSessionSheet";
import { SessionList } from "./components/SessionList";
import { SettingsPage, type ThemePref } from "./components/SettingsPage";
import { TabBar, type Tab } from "./components/TabBar";
import { TopBar } from "./components/TopBar";

function applyTheme(pref: ThemePref) {
  const root = document.documentElement;
  if (pref === "system") {
    const light = window.matchMedia("(prefers-color-scheme: light)").matches;
    root.setAttribute("data-theme", light ? "light" : "dark");
  } else {
    root.setAttribute("data-theme", pref);
  }
}

function useWide(): boolean {
  const [wide, setWide] = useState(
    () => typeof window !== "undefined" && window.matchMedia("(min-width: 900px)").matches,
  );
  useEffect(() => {
    const mq = window.matchMedia("(min-width: 900px)");
    const on = () => setWide(mq.matches);
    on();
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, []);
  return wide;
}

export function App() {
  const [locale, setLocale] = useState<Locale>(() => resolveLocale(navigator.language));
  const [theme, setTheme] = useState<ThemePref>("system");
  const [platform, setPlatform] = useState<Platform>(() => detectPlatform());
  const [tab, setTab] = useState<Tab>("sessions");
  const [sessions, setSessions] = useState<SessionDescriptor[]>([]);
  const [backends, setBackends] = useState<Record<string, SessionBackend>>({});
  const [activeId, setActiveId] = useState<string | null>(null);
  const [linesById, setLinesById] = useState<Record<string, ChatLine[]>>({});
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const [sheetOpen, setSheetOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);

  const localBackend = useMemo(() => new LocalBackend(), []);
  const wide = useWide();
  const active = sessions.find((s) => s.id === activeId) ?? null;
  const activeBackend = activeId ? backends[activeId] : undefined;
  const lines = activeId ? linesById[activeId] ?? [] : [];
  const chatOpen = Boolean(active) && (wide || tab === "sessions");

  useEffect(() => {
    applyPlatform(platform);
  }, [platform]);

  useEffect(() => {
    applyTheme(theme);
    if (theme !== "system") return;
    const mq = window.matchMedia("(prefers-color-scheme: light)");
    const onChange = () => applyTheme("system");
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, [theme]);

  const openNewSheet = useCallback(() => {
    setCreateError(null);
    setSheetOpen(true);
  }, []);

  const createSession = useCallback(
    async (input: { runtime: SessionRuntime; nodeUrl?: string }) => {
      setCreating(true);
      setCreateError(null);
      try {
        let backend: SessionBackend;
        let d: SessionDescriptor;
        if (input.runtime === "local") {
          backend = localBackend;
          d = await backend.createSession({
            runtime: "local",
            title: t(locale, "sessions.runtimeLocal"),
          });
        } else {
          const url = (input.nodeUrl || "http://127.0.0.1:8790").replace(/\/$/, "");
          backend = new RemoteBackend(url);
          d = await backend.createSession({
            runtime: "remote",
            title: t(locale, "sessions.runtimeRemote"),
          });
        }
        setBackends((prev) => ({ ...prev, [d.id]: backend }));
        setSessions((prev) => [d, ...prev]);
        setLinesById((prev) => ({ ...prev, [d.id]: [] }));
        setActiveId(d.id);
        setDraft("");
        setTab("sessions");
        setSheetOpen(false);
      } catch (err) {
        const msg = err instanceof Error ? err.message : t(locale, "sessions.createError");
        setCreateError(msg);
      } finally {
        setCreating(false);
      }
    },
    [localBackend, locale],
  );

  const send = useCallback(async () => {
    if (!active || !activeBackend || !draft.trim() || sending) return;
    const text = draft.trim();
    const sessionId = active.id;
    setDraft("");
    setSending(true);
    setLinesById((prev) => ({
      ...prev,
      [sessionId]: [
        ...(prev[sessionId] ?? []),
        { id: `u-${Date.now()}`, kind: "user", text, role: "user" },
      ],
    }));
    const unsub = activeBackend.subscribe(sessionId, (event, seq) => {
      const e = event as { kind?: string; text?: string };
      const kind = e.kind || "event";
      let role: ChatLine["role"] = "assistant";
      if (kind === "notice" || kind.startsWith("tool_")) role = "tool";
      if (kind === "turn_started" || kind === "turn_done") role = "system";
      const textOut =
        e.text ||
        (kind === "turn_started"
          ? "…"
          : kind === "turn_done"
            ? "✓"
            : JSON.stringify(event));
      setLinesById((prev) => ({
        ...prev,
        [sessionId]: [
          ...(prev[sessionId] ?? []),
          { id: `e-${seq}-${kind}`, kind, text: textOut, role },
        ],
      }));
    });
    try {
      await activeBackend.submit(sessionId, { text }, `req_${Date.now()}`);
      const snap = await activeBackend.snapshot(sessionId);
      setSessions((prev) =>
        prev.map((s) => (s.id === sessionId ? snap.descriptor : s)),
      );
    } catch (err) {
      const msg = err instanceof Error ? err.message : "send failed";
      setLinesById((prev) => ({
        ...prev,
        [sessionId]: [
          ...(prev[sessionId] ?? []),
          { id: `err-${Date.now()}`, kind: "notice", text: msg, role: "tool" },
        ],
      }));
    } finally {
      unsub();
      setSending(false);
    }
  }, [active, activeBackend, draft, sending]);

  const selectSession = (id: string) => {
    setActiveId(id);
    setDraft("");
    setTab("sessions");
  };

  const iosChrome = platform === "ios";

  const listPane = (
    <div className="session-list-pane">
      <TopBar
        title={t(locale, "sessions.title")}
        largeTitle={iosChrome}
        trailing={
          <IconButton label={t(locale, "sessions.new")} onClick={openNewSheet}>
            <Plus size={22} strokeWidth={2.25} />
          </IconButton>
        }
      />
      <div className="page-scroll">
        {iosChrome ? <h1 className="large-title">{t(locale, "sessions.title")}</h1> : null}
        {sessions.length === 0 ? (
          <EmptyState
            icon={<MessageSquare size={28} strokeWidth={1.75} />}
            title={t(locale, "sessions.emptyTitle")}
            description={t(locale, "sessions.empty")}
            actions={
              <button type="button" className="btn-primary" onClick={openNewSheet}>
                <Plus size={18} />
                {t(locale, "sessions.new")}
              </button>
            }
          />
        ) : (
          <SessionList
            sessions={sessions}
            activeId={activeId}
            locale={locale}
            onSelect={selectSession}
          />
        )}
      </div>
      <button
        type="button"
        className="fab"
        aria-label={t(locale, "sessions.new")}
        onClick={openNewSheet}
      >
        <Plus size={26} strokeWidth={2.25} />
      </button>
    </div>
  );

  const detailPane = active ? (
    <div className="session-detail-pane">
      <ChatView
        session={active}
        lines={lines}
        draft={draft}
        sending={sending}
        locale={locale}
        showBack={!wide}
        onBack={() => setActiveId(null)}
        onDraftChange={setDraft}
        onSend={() => void send()}
      />
    </div>
  ) : wide ? (
    <div className="session-detail-pane session-detail-placeholder">
      {t(locale, "sessions.selectHint")}
    </div>
  ) : null;

  return (
    <div
      className="app-shell"
      data-chat-open={chatOpen && !wide ? "true" : "false"}
      data-wide={wide ? "true" : "false"}
    >
      <div className="app-body">
        {tab === "sessions" && (
          <div
            className="sessions-root sessions-split"
            data-detail={active ? "true" : "false"}
          >
            {listPane}
            {detailPane}
          </div>
        )}

        {tab === "nodes" && (
          <div className="page">
            <TopBar title={t(locale, "nodes.title")} largeTitle={iosChrome} />
            <div className="page-scroll">
              {iosChrome ? <h1 className="large-title">{t(locale, "nodes.title")}</h1> : null}
              <EmptyState
                icon={<Server size={28} strokeWidth={1.75} />}
                title={t(locale, "nodes.emptyTitle")}
                description={t(locale, "nodes.empty")}
                actions={
                  <>
                    <button type="button" className="btn-primary" disabled>
                      {t(locale, "nodes.pair")}
                    </button>
                    <p className="empty-desc" style={{ marginTop: 4 }}>
                      {t(locale, "nodes.pairHint")}
                    </p>
                  </>
                }
              />
            </div>
          </div>
        )}

        {tab === "providers" && (
          <div className="page">
            <TopBar title={t(locale, "providers.title")} largeTitle={iosChrome} />
            <div className="page-scroll">
              {iosChrome ? (
                <h1 className="large-title">{t(locale, "providers.title")}</h1>
              ) : null}
              <EmptyState
                icon={<Sparkles size={28} strokeWidth={1.75} />}
                title={t(locale, "providers.emptyTitle")}
                description={t(locale, "providers.empty")}
                actions={
                  <button type="button" className="btn-primary" disabled>
                    {t(locale, "providers.add")}
                  </button>
                }
              />
            </div>
          </div>
        )}

        {tab === "settings" && (
          <div className="page">
            <TopBar title={t(locale, "settings.title")} largeTitle={iosChrome} />
            <SettingsPage
              locale={locale}
              theme={theme}
              platform={platform}
              showLargeTitle={iosChrome}
              onLocale={setLocale}
              onTheme={setTheme}
              onPlatform={setPlatform}
            />
          </div>
        )}
      </div>

      <TabBar
        tab={tab}
        locale={locale}
        onChange={(next) => {
          setTab(next);
          if (next !== "sessions" && !wide) setActiveId(null);
        }}
      />

      <NewSessionSheet
        open={sheetOpen}
        locale={locale}
        busy={creating}
        error={createError}
        onClose={() => !creating && setSheetOpen(false)}
        onCreate={(input) => void createSession(input)}
      />
    </div>
  );
}
