import { AlertTriangle, MessageSquarePlus, PanelRightClose, Plus, TerminalSquare } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { useT } from "../lib/i18n";
import { startTerminalEventBridge } from "../lib/terminalEvents";
import { useTerminalStore } from "../store/terminal";
import { TerminalSessionRail } from "./TerminalSessionRail";
import { TerminalView } from "./TerminalView";

export function TerminalPanel({
  tabId,
  cwd,
  onClose,
  onAddOutput,
}: {
  tabId: string;
  cwd?: string;
  onClose: () => void;
  onAddOutput: (sessionId: string) => void;
}) {
  const t = useT();
  const [selectedShellId, setSelectedShellId] = useState("default");
  const workspace = useTerminalStore((state) => state.workspace);
  const activeSessionId = useTerminalStore((state) => state.activeSessionId);
  const syncWorkspace = useTerminalStore((state) => state.syncWorkspace);
  const createSession = useTerminalStore((state) => state.createSession);
  const closeSession = useTerminalStore((state) => state.closeSession);
  const setActiveSession = useTerminalStore((state) => state.setActiveSession);

  useEffect(() => {
    startTerminalEventBridge();
    void syncWorkspace(tabId).catch(() => {});
  }, [syncWorkspace, tabId]);

  useEffect(() => {
    if (workspace && !workspace.shells.some((shell) => shell.id === selectedShellId)) {
      setSelectedShellId("default");
    }
  }, [selectedShellId, workspace]);

  const newSession = useCallback(() => {
    void createSession(tabId, ".", selectedShellId).catch(() => {});
  }, [createSession, selectedShellId, tabId]);
  const sessions = workspace?.sessions ?? [];
  const shellOptions = workspace?.shells.length
    ? workspace.shells
    : [{ id: "default", label: t("terminal.defaultShell") }];
  const active = sessions.find((session) => session.id === activeSessionId) ?? sessions[0];

  return (
    <section className="terminal-panel" aria-label={t("terminal.title")}>
      <header className="terminal-panel__header">
        <div className="terminal-panel__identity"><TerminalSquare size={15} /><strong>{t("terminal.title")}</strong>{cwd && <span title={cwd}>{cwd}</span>}</div>
        <div className="terminal-panel__actions">
          <select
            className="terminal-shell-select"
            value={selectedShellId}
            onChange={(event) => setSelectedShellId(event.target.value)}
            disabled={!workspace?.available || workspace.readOnly}
            aria-label={t("terminal.shell")}
            title={t("terminal.shell")}
          >
            {shellOptions.map((shell) => (
              <option key={shell.id} value={shell.id}>
                {shell.id === "default" ? t("terminal.defaultShell") : shell.label}
              </option>
            ))}
          </select>
          <button type="button" className="terminal-icon-button" onClick={newSession} disabled={!workspace?.available || workspace.readOnly} aria-label={t("terminal.newSession")} title={t("terminal.newSession")}><Plus size={15} /></button>
          <button type="button" className="terminal-icon-button" onClick={() => active && onAddOutput(active.id)} disabled={!active} aria-label={t("terminal.addOutput")} title={t("terminal.addOutput")}><MessageSquarePlus size={15} /></button>
          <button type="button" className="terminal-icon-button" onClick={onClose} aria-label={t("rightDock.collapse")} title={t("rightDock.collapse")}><PanelRightClose size={15} /></button>
        </div>
      </header>
      {!workspace ? (
        <div className="terminal-empty"><span className="terminal-empty__spinner" />{t("terminal.loading")}</div>
      ) : !workspace.available || workspace.readOnly ? (
        <div className="terminal-empty"><AlertTriangle size={18} /><strong>{workspace.reason || t("terminal.readOnly")}</strong></div>
      ) : (
        <div className="terminal-panel__body">
          {sessions.length > 1 && (
            <TerminalSessionRail
              sessions={sessions}
              activeSessionId={active?.id ?? null}
              onSelect={setActiveSession}
              onClose={(id) => void closeSession(tabId, id).catch(() => {})}
            />
          )}
          <div className="terminal-panel__content">
            {active ? <TerminalView key={active.id} tabId={tabId} session={active} /> : (
              <div className="terminal-empty terminal-empty--action"><TerminalSquare size={22} /><p>{t("terminal.empty")}</p><button type="button" className="btn btn--secondary btn--small" onClick={newSession}><Plus size={14} />{t("terminal.newSession")}</button></div>
            )}
          </div>
        </div>
      )}
    </section>
  );
}
