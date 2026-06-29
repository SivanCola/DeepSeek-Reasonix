import { Maximize2, Minimize2, Plus, TerminalSquare, X } from "lucide-react";
import { useCallback } from "react";

import { useT } from "../lib/i18n";
import { useTerminalStore } from "../store/terminal";
import { TerminalSessionRail } from "./TerminalSessionRail";
import { TerminalView } from "./TerminalView";
import { Tooltip } from "./Tooltip";

type TerminalPanelProps = {
  readOnly?: boolean;
  variant?: "dock";
  maximized?: boolean;
  onToggleMaximized?: () => void;
  onClose?: () => void;
};

export function TerminalPanel({
  readOnly = false,
  variant = "dock",
  maximized = false,
  onToggleMaximized,
  onClose,
}: TerminalPanelProps) {
  const t = useT();
  const sessions = useTerminalStore((s) => s.sessions);
  const activeSessionId = useTerminalStore((s) => s.activeSessionId);
  const busy = useTerminalStore((s) => s.busy);
  const createSession = useTerminalStore((s) => s.createSession);
  const closeSession = useTerminalStore((s) => s.closeSession);
  const setActiveSession = useTerminalStore((s) => s.setActiveSession);
  const refreshSession = useTerminalStore((s) => s.refreshSession);

  const activeSession = sessions.find((s) => s.id === activeSessionId) ?? null;

  const handleExit = useCallback(
    (sessionId: string, exitCode: number) => {
      refreshSession(sessionId, { running: false, exitCode });
    },
    [refreshSession],
  );

  const handleCreate = (shellPrefer?: string) => {
    void createSession(undefined, undefined, shellPrefer);
  };

  const handleRestart = () => {
    if (!activeSession) return;
    void closeSession(activeSession.id).then(() => createSession(activeSession.title));
  };

  if (readOnly) {
    return (
      <section
        className={["terminal-panel", variant === "dock" ? "terminal-panel--dock" : ""].filter(Boolean).join(" ")}
        aria-label={t("terminal.panel")}
      >
        <div className="terminal-panel__empty">{t("terminal.readOnly")}</div>
      </section>
    );
  }

  return (
    <section
      className={[
        "terminal-panel",
        variant === "dock" ? "terminal-panel--dock" : "",
        maximized ? "terminal-panel--maximized" : "",
      ].filter(Boolean).join(" ")}
      aria-label={t("terminal.panel")}
    >
      <header className="terminal-panel__toolbar">
        <div className="terminal-panel__toolbar-left">
          <TerminalSquare size={14} aria-hidden="true" />
          <span className="terminal-panel__toolbar-title">
            {activeSession?.title ?? t("terminal.panel")}
          </span>
          {activeSession && !activeSession.running && (
            <button type="button" className="terminal-panel__restart" onClick={handleRestart}>
              {t("terminal.restart")}
            </button>
          )}
        </div>
        <div className="terminal-panel__toolbar-actions">
          <Tooltip label={t("terminal.newSession")}>
            <button type="button" className="terminal-panel__icon-btn" aria-label={t("terminal.newSession")} disabled={busy} onClick={() => handleCreate("login")}>
              <Plus size={14} />
            </button>
          </Tooltip>
          {onToggleMaximized && (
            <Tooltip label={maximized ? t("terminal.restore") : t("terminal.maximize")}>
              <button
                type="button"
                className="terminal-panel__icon-btn"
                aria-label={maximized ? t("terminal.restore") : t("terminal.maximize")}
                onClick={onToggleMaximized}
              >
                {maximized ? <Minimize2 size={14} /> : <Maximize2 size={14} />}
              </button>
            </Tooltip>
          )}
          {onClose && (
            <Tooltip label={t("terminal.closePanel")}>
              <button type="button" className="terminal-panel__icon-btn" aria-label={t("terminal.closePanel")} onClick={onClose}>
                <X size={14} />
              </button>
            </Tooltip>
          )}
        </div>
      </header>
      <div className="terminal-panel__body">
        <TerminalSessionRail
          sessions={sessions}
          activeSessionId={activeSessionId}
          onSelect={setActiveSession}
          onClose={(id) => void closeSession(id)}
          onCreate={handleCreate}
          disabled={busy}
        />
        <div className="terminal-panel__views">
          {sessions.length === 0 ? (
            <div className="terminal-panel__empty">
              <p>{t("terminal.empty")}</p>
              <button type="button" className="terminal-panel__empty-action" disabled={busy} onClick={() => handleCreate("login")}>
                {t("terminal.newSession")}
              </button>
            </div>
          ) : (
            sessions.map((session) => (
              <div
                key={session.id}
                className={[
                  "terminal-panel__view-slot",
                  session.id === activeSessionId ? "terminal-panel__view-slot--active" : "",
                ].filter(Boolean).join(" ")}
                aria-hidden={session.id !== activeSessionId}
              >
                <TerminalView
                  sessionId={session.id}
                  active={session.id === activeSessionId}
                  onExit={(exitCode) => handleExit(session.id, exitCode)}
                />
              </div>
            ))
          )}
        </div>
      </div>
    </section>
  );
}
