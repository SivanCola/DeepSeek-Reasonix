// theme.ts manages the appearance override. Mode controls light/dark behavior;
// scheme controls accent color only, so palette changes do not disturb the base
// app surfaces. The choice persists in localStorage and is applied on load.

export type ThemeMode = "auto" | "light" | "dark";
export type ThemeScheme = "clay" | "blue" | "forest" | "amber" | "teal";
export type Theme = {
  mode: ThemeMode;
  scheme: ThemeScheme;
};

const KEY = "reasonix-theme";
const MODES: ThemeMode[] = ["auto", "light", "dark"];
const SCHEMES: ThemeScheme[] = ["clay", "blue", "forest", "amber", "teal"];
const DEFAULT_THEME: Theme = { mode: "auto", scheme: "clay" };

function normalizeTheme(value: unknown): Theme | null {
  if (typeof value === "object" && value !== null) {
    const candidate = value as Partial<Theme>;
    if (MODES.includes(candidate.mode as ThemeMode) && SCHEMES.includes(candidate.scheme as ThemeScheme)) {
      return { mode: candidate.mode as ThemeMode, scheme: candidate.scheme as ThemeScheme };
    }
  }
  if (typeof value !== "string") return null;
  switch (value) {
    case "auto":
      return DEFAULT_THEME;
    case "light":
      return { mode: "light", scheme: "clay" };
    case "dark":
      return { mode: "dark", scheme: "clay" };
    case "focus":
      return { mode: "light", scheme: "blue" };
    case "forest":
      return { mode: "light", scheme: "forest" };
    case "midnight":
      return { mode: "dark", scheme: "amber" };
    case "contrast":
      return { mode: "dark", scheme: "teal" };
    default:
      return null;
  }
}

export function getTheme(): Theme {
  const v = typeof localStorage !== "undefined" ? localStorage.getItem(KEY) : null;
  if (!v) return DEFAULT_THEME;
  try {
    const parsed = JSON.parse(v) as unknown;
    return normalizeTheme(parsed) ?? normalizeTheme(v) ?? DEFAULT_THEME;
  } catch {
    return normalizeTheme(v) ?? DEFAULT_THEME;
  }
}

export function applyTheme(theme: Theme): void {
  if (typeof document === "undefined") return;
  const root = document.documentElement;
  root.removeAttribute("data-theme");
  if (theme.mode === "auto") root.removeAttribute("data-theme-mode");
  else root.setAttribute("data-theme-mode", theme.mode);
  root.setAttribute("data-theme-scheme", theme.scheme);
  try {
    localStorage.setItem(KEY, JSON.stringify(theme));
  } catch {
    /* private mode / no storage — the in-DOM attribute still applies */
  }
}

// initTheme applies the saved choice once at startup (before React renders).
export function initTheme(): void {
  applyTheme(getTheme());
}
