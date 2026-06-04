// theme.ts manages the appearance override. The stylesheet follows the OS via
// prefers-color-scheme unless data-theme forces "dark" or "light". A separate
// data-theme-style attribute changes only accent tokens.
//
// When running inside the Wails shell, applyTheme also syncs the native window
// theme (title bar, traffic lights, etc.) so the OS chrome matches the webview.

import {
  WindowSetDarkTheme,
  WindowSetLightTheme,
  WindowSetSystemDefaultTheme,
  WindowSetBackgroundColour,
} from "../../wailsjs/runtime/runtime";

export type Theme = "auto" | "light" | "dark";
export type ResolvedTheme = Exclude<Theme, "auto">;

export const THEME_STYLES = [
  "graphite",
  "ember",
  "aurora",
  "midnight",
  "sandstone",
  "porcelain",
  "linen",
  "glacier",
] as const;

export type ThemeStyle = (typeof THEME_STYLES)[number];

export const THEME_STYLE_THEME: Record<ThemeStyle, ResolvedTheme> = {
  graphite: "dark",
  ember: "dark",
  aurora: "dark",
  midnight: "dark",
  sandstone: "light",
  porcelain: "light",
  linen: "light",
  glacier: "light",
};

const DEFAULT_THEME_STYLE: Record<ResolvedTheme, ThemeStyle> = {
  dark: "graphite",
  light: "glacier",
};
// New users default to "auto" so the app follows the OS preference. Existing users
// with an explicit light/dark stored in localStorage keep their choice unchanged.
const DEFAULT_THEME: Theme = "auto";

const THEME_KEY = "reasonix-theme";
const STYLE_KEY = "reasonix-theme-style";

function normalizeTheme(value: unknown): Theme | null {
  if (typeof value === "object" && value !== null) {
    return normalizeTheme((value as { mode?: unknown }).mode);
  }
  if (typeof value !== "string") return null;
  switch (value) {
    case "auto":
      return "auto";
    case "light":
    case "focus":
    case "forest":
      return "light";
    case "dark":
    case "midnight":
    case "contrast":
      return "dark";
    default:
      return null;
  }
}

export function isThemeStyle(value: unknown): value is ThemeStyle {
  return typeof value === "string" && (THEME_STYLES as readonly string[]).includes(value);
}

export function getTheme(): Theme {
  const v = typeof localStorage !== "undefined" ? localStorage.getItem(THEME_KEY) : null;
  if (!v) return DEFAULT_THEME;
  try {
    const parsed = JSON.parse(v) as unknown;
    return normalizeTheme(parsed) ?? normalizeTheme(v) ?? DEFAULT_THEME;
  } catch {
    return normalizeTheme(v) ?? DEFAULT_THEME;
  }
}

export function getResolvedTheme(theme: Theme = getTheme()): ResolvedTheme {
  if (theme === "light" || theme === "dark") return theme;
  if (typeof window !== "undefined" && window.matchMedia?.("(prefers-color-scheme: light)").matches) return "light";
  return "dark";
}

export function defaultStyleForTheme(theme: Theme): ThemeStyle {
  return DEFAULT_THEME_STYLE[getResolvedTheme(theme)];
}

export function themeForStyle(style: ThemeStyle): ResolvedTheme {
  return THEME_STYLE_THEME[style];
}

export function getThemeStyle(theme: Theme = getTheme()): ThemeStyle {
  const stored = typeof localStorage !== "undefined" ? localStorage.getItem(STYLE_KEY) : null;
  if (isThemeStyle(stored) && themeForStyle(stored) === getResolvedTheme(theme)) return stored;
  return defaultStyleForTheme(theme);
}

export function applyTheme(theme: Theme, style: ThemeStyle = getThemeStyle(theme), options: { persist?: boolean } = {}): void {
  if (typeof document === "undefined") return;
  const root = document.documentElement;
  root.removeAttribute("data-theme-mode");
  root.removeAttribute("data-theme-scheme");
  if (theme === "auto") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", theme);

  const resolved = getResolvedTheme(theme);
  const nextStyle = themeForStyle(style) === resolved ? style : DEFAULT_THEME_STYLE[resolved];
  root.setAttribute("data-theme-style", nextStyle);

  // Sync the native window theme (title bar, traffic lights) to match.
  if (typeof window !== "undefined" && window.runtime) {
    if (theme === "auto") {
      WindowSetSystemDefaultTheme();
    } else if (theme === "light") {
      WindowSetLightTheme();
    } else if (theme === "dark") {
      WindowSetDarkTheme();
    }
  }

  if (options.persist === false) return;
  try {
    localStorage.setItem(THEME_KEY, theme);
    localStorage.setItem(STYLE_KEY, nextStyle);
  } catch {
    /* private mode / no storage — the in-DOM attributes still apply */
  }
}

// initTheme runs before React mounts. It applies the saved theme to the DOM and
// sets the native window background colour to match the resolved theme, avoiding
// a white (or wrong-colour) flash while the webview paints its first frame.
export function initTheme(): void {
  const theme = getTheme();
  applyTheme(theme, getThemeStyle(theme), { persist: false });

  if (typeof window !== "undefined" && window.runtime) {
    const resolved = getResolvedTheme(theme);
    if (resolved === "light") {
      // Light shell: matches :root[data-theme="light"] --bg (#f7f8fb).
      WindowSetBackgroundColour(247, 248, 251, 255);
    } else {
      // Dark shell: matches :root --bg (#090a0c).
      WindowSetBackgroundColour(9, 10, 12, 255);
    }
  }
}
