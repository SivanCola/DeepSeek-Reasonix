import { TerminalSquare, X } from "lucide-react";
import { useState, type MouseEvent as ReactMouseEvent } from "react";

import { useT } from "../lib/i18n";
import type { TerminalSessionView } from "../lib/types";
import { FloatingMenu, FloatingMenuItems } from "./FloatingMenu";
import { Tooltip } from "./Tooltip";

type TerminalSessionRailProps = {
  sessions: TerminalSessionView[];
  activeSessionId: string | null;
  onSelect: (id: string) => void;
  onClose: (id: string) => void;
  onCreate: (shellPrefer?: string) => void;
  disabled?: boolean;
};

export function TerminalSessionRail({
  sessions,
  activeSessionId,
  onSelect,
  onClose,
  onCreate,
  disabled = false,
}: TerminalSessionRailProps) {
  const t = useT();
  const [newMenu, setNewMenu] = useState<{ x: number; y: number } | null>(null);

  const handleClose = (event: ReactMouseEvent, id: string) => {
    event.stopPropagation();
    onClose(id);
  };

  const openNewMenu = (event: ReactMouseEvent<HTMLButtonElement>) => {
    if (disabled) return;
    const rect = event.currentTarget.getBoundingClientRect();
    setNewMenu({ x: rect.left, y: rect.bottom + 6 });
  };

  const shellItems = [
    {
      label: t("terminal.shellLogin"),
      onSelect: () => {
        setNewMenu(null);
        onCreate("login");
      },
    },
    {
      label: "zsh",
      onSelect: () => {
        setNewMenu(null);
        onCreate("zsh");
      },
    },
    {
      label: "bash",
      onSelect: () => {
        setNewMenu(null);
        onCreate("bash");
      },
    },
  ];

  return (
    <aside className="terminal-rail" aria-label={t("terminal.sessions")}>
      <div className="terminal-rail__head">
        <span className="terminal-rail__title">{t("terminal.sessionCount", { n: sessions.length })}</span>
        <Tooltip label={t("terminal.newSession")}>
          <button
            type="button"
            className="terminal-rail__new"
            aria-label={t("terminal.newSession")}
            disabled={disabled}
            onClick={openNewMenu}
          >
            +
          </button>
        </Tooltip>
      </div>
      <div className="terminal-rail__list" role="listbox" aria-label={t("terminal.sessions")}>
        {sessions.map((session) => {
          const active = session.id === activeSessionId;
          return (
            <button
              key={session.id}
              type="button"
              role="option"
              aria-selected={active}
              className={[
                "terminal-rail__item",
                active ? "terminal-rail__item--active" : "",
                !session.running ? "terminal-rail__item--exited" : "",
              ].filter(Boolean).join(" ")}
              onClick={() => onSelect(session.id)}
            >
              <TerminalSquare size={13} aria-hidden="true" />
              <span className="terminal-rail__item-label">{session.title}</span>
              <span
                className="terminal-rail__item-close"
                role="button"
                tabIndex={-1}
                aria-label={t("terminal.closeSession")}
                onClick={(event) => handleClose(event, session.id)}
              >
                <X size={12} />
              </span>
            </button>
          );
        })}
      </div>
      {newMenu && (
        <>
          <button type="button" className="terminal-rail__menu-backdrop" aria-label={t("common.close")} onClick={() => setNewMenu(null)} />
          <FloatingMenu x={newMenu.x} y={newMenu.y} width={180} estimatedHeight={120} className="terminal-rail__menu">
            <FloatingMenuItems items={shellItems} />
          </FloatingMenu>
        </>
      )}
    </aside>
  );
}
