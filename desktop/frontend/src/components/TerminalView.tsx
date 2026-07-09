import { useCallback, useEffect, useRef } from "react";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";

import { app } from "../lib/bridge";
import { terminalThemeFromDocument } from "../lib/terminalTheme";
import { useTerminalExit, useTerminalOutput } from "../lib/terminalBridge";

type TerminalViewProps = {
  sessionId: string;
  active: boolean;
  onExit: (exitCode: number) => void;
};

export function TerminalView({ sessionId, active, onExit }: TerminalViewProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const activeRef = useRef(active);
  const onExitRef = useRef(onExit);
  activeRef.current = active;
  onExitRef.current = onExit;

  const handleOutput = useCallback((text: string) => {
    termRef.current?.write(text);
  }, []);

  useTerminalOutput(sessionId, handleOutput);
  useTerminalExit(sessionId, (exitCode) => onExitRef.current(exitCode));

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    const term = new Terminal({
      convertEol: false,
      cursorBlink: true,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
      fontSize: 13,
      lineHeight: 1.25,
      theme: terminalThemeFromDocument(),
      scrollback: 5000,
    });
    const fitAddon = new FitAddon();
    const linksAddon = new WebLinksAddon();
    term.loadAddon(fitAddon);
    term.loadAddon(linksAddon);
    term.open(container);
    fitAddon.fit();
    void app.ResizeTerminal(sessionId, term.cols, term.rows);

    term.onData((data) => {
      void app.WriteTerminal(sessionId, data);
    });

    termRef.current = term;
    fitRef.current = fitAddon;

    const syncTheme = () => term.options.theme = terminalThemeFromDocument();
    const observer = new MutationObserver(syncTheme);
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme", "data-theme-style"] });

    const resizeObserver = new ResizeObserver(() => {
      if (!activeRef.current) return;
      fitAddon.fit();
      void app.ResizeTerminal(sessionId, term.cols, term.rows);
    });
    resizeObserver.observe(container);

    return () => {
      observer.disconnect();
      resizeObserver.disconnect();
      term.dispose();
      termRef.current = null;
      fitRef.current = null;
    };
  }, [sessionId]);

  useEffect(() => {
    if (!active) return;
    const term = termRef.current;
    const fitAddon = fitRef.current;
    if (!term || !fitAddon) return;
    requestAnimationFrame(() => {
      fitAddon.fit();
      void app.ResizeTerminal(sessionId, term.cols, term.rows);
      term.focus();
    });
  }, [active, sessionId]);

  return <div className="terminal-view" ref={containerRef} />;
}
