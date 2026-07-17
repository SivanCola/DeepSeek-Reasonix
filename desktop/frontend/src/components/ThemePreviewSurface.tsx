import { useMemo, type CSSProperties } from "react";
import type { ThemePackView } from "../lib/themePack";
import { isThemeStyle, type ThemeStyle } from "../lib/theme";

/** Isolated mini Reasonix surface for gallery detail — does not touch :root. */
export function ThemePreviewSurface({
  pack,
  mode,
  scene,
}: {
  pack: ThemePackView | null;
  mode: "light" | "dark";
  scene: "home" | "task";
}) {
  const style = useMemo(() => {
    const tokens = mode === "light" ? pack?.tokens?.light : pack?.tokens?.dark;
    const bg = tokens?.bg || (mode === "light" ? "#f4f3ef" : "#0c0d10");
    const panel = tokens?.panel || tokens?.bgElev || (mode === "light" ? "#ffffff" : "#1a1b1f");
    const sidebar = tokens?.sidebar || (mode === "light" ? "#f0efe9" : "#14151a");
    const fg = tokens?.fg || (mode === "light" ? "#111827" : "#f1f1ef");
    const fgDim = tokens?.fgDim || (mode === "light" ? "#6b7280" : "#9ca3af");
    const accent = tokens?.accent || "#ff6a3d";
    const accentFg = tokens?.accentFg || "#ffffff";
    const border = tokens?.border || (mode === "light" ? "#e5e2da" : "#2a2b31");
    const chat = tokens?.chat || bg;
    const base = isThemeStyle(pack?.baseStyle) ? (pack!.baseStyle as ThemeStyle) : "graphite";
    const focusX = pack?.background?.focusX ?? 0.72;
    const focusY = pack?.background?.focusY ?? 0.45;
    const opacity =
      scene === "home"
        ? pack?.background?.homeOpacity ?? 1
        : pack?.background?.taskOpacity ?? 0.28;
    const overlay = pack?.background?.overlayStrength ?? 0.62;
    return {
      ["--tp-bg" as string]: bg,
      ["--tp-panel" as string]: panel,
      ["--tp-sidebar" as string]: sidebar,
      ["--tp-fg" as string]: fg,
      ["--tp-fg-dim" as string]: fgDim,
      ["--tp-accent" as string]: accent,
      ["--tp-accent-fg" as string]: accentFg,
      ["--tp-border" as string]: border,
      ["--tp-chat" as string]: chat,
      ["--tp-focus-x" as string]: `${focusX * 100}%`,
      ["--tp-focus-y" as string]: `${focusY * 100}%`,
      ["--tp-bg-opacity" as string]: String(opacity),
      ["--tp-overlay" as string]: String(overlay),
      ["--tp-base" as string]: base,
    } as CSSProperties;
  }, [pack, mode, scene]);

  const bgUrl = pack?.previewUrl || pack?.backgroundUrl || "";

  return (
    <div className="theme-preview-surface" data-mode={mode} data-scene={scene} style={style}>
      {bgUrl ? (
        <div
          className="theme-preview-surface__bg"
          style={{ backgroundImage: `url("${bgUrl}")` }}
          aria-hidden="true"
        />
      ) : (
        <div className="theme-preview-surface__bg theme-preview-surface__bg--swatch" aria-hidden="true" />
      )}
      <div className="theme-preview-surface__overlay" aria-hidden="true" />
      <div className="theme-preview-surface__chrome">
        <aside className="theme-preview-surface__side">
          <div className="theme-preview-surface__logo">R</div>
          <div className="theme-preview-surface__nav" />
          <div className="theme-preview-surface__nav theme-preview-surface__nav--dim" />
          <div className="theme-preview-surface__nav theme-preview-surface__nav--dim" />
        </aside>
        <main className="theme-preview-surface__main">
          <div className="theme-preview-surface__card">
            <div className="theme-preview-surface__title" />
            <div className="theme-preview-surface__line" />
            <div className="theme-preview-surface__line theme-preview-surface__line--short" />
            <div className="theme-preview-surface__cta" />
          </div>
        </main>
      </div>
    </div>
  );
}
