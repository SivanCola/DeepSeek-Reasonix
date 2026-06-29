import type { ITheme } from "@xterm/xterm";

function cssVar(name: string, fallback: string): string {
  if (typeof document === "undefined") return fallback;
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return value || fallback;
}

export function terminalThemeFromDocument(): ITheme {
  return {
    background: cssVar("--bg", "#090a0c"),
    foreground: cssVar("--fg", "#e8e6e3"),
    cursor: cssVar("--accent", "#7c9cff"),
    cursorAccent: cssVar("--bg", "#090a0c"),
    selectionBackground: cssVar("--accent-soft", "rgba(124, 156, 255, 0.28)"),
    black: cssVar("--term-black", "#1e1f24"),
    red: cssVar("--term-red", "#f07178"),
    green: cssVar("--term-green", "#98c379"),
    yellow: cssVar("--term-yellow", "#e5c07b"),
    blue: cssVar("--term-blue", "#61afef"),
    magenta: cssVar("--term-magenta", "#c678dd"),
    cyan: cssVar("--term-cyan", "#56b6c2"),
    white: cssVar("--term-white", "#abb2bf"),
    brightBlack: cssVar("--term-bright-black", "#5c6370"),
    brightRed: cssVar("--term-bright-red", "#ff7b86"),
    brightGreen: cssVar("--term-bright-green", "#b5e890"),
    brightYellow: cssVar("--term-bright-yellow", "#ffd68a"),
    brightBlue: cssVar("--term-bright-blue", "#8cc8ff"),
    brightMagenta: cssVar("--term-bright-magenta", "#de91f0"),
    brightCyan: cssVar("--term-bright-cyan", "#7fd4de"),
    brightWhite: cssVar("--term-bright-white", "#ffffff"),
  };
}
