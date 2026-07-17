// Run: tsx src/__tests__/theme-pack.test.ts

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  applyThemePack,
  applyThemeScene,
  beginThemePreview,
  cancelThemePreview,
  clearThemePack,
  draftPackView,
  isSafeBackgroundURL,
  isSafeHex,
  isThemeTokenKey,
  setBaseAppearance,
} from "../lib/themePack";
import { applyTheme, getThemeStyle } from "../lib/theme";

const testDir = dirname(fileURLToPath(import.meta.url));
const packSource = readFileSync(resolve(testDir, "../lib/themePack.ts"), "utf8");
const stylesSource = readFileSync(resolve(testDir, "../styles.css"), "utf8");
const appSource = readFileSync(resolve(testDir, "../App.tsx"), "utf8");
const librarySource = readFileSync(resolve(testDir, "../components/ThemeLibrary.tsx"), "utf8");

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

// Minimal DOM for applyThemePack
const attrs = new Map<string, string>();
const styleProps = new Map<string, string>();
const styleEl: {
  id: string;
  textContent: string;
  parentElement: { removeChild: (el: unknown) => void } | null;
  remove: () => void;
} = {
  id: "",
  textContent: "",
  parentElement: null,
  remove() {
    const idx = headChildren.indexOf(styleEl);
    if (idx >= 0) headChildren.splice(idx, 1);
    styleEl.textContent = "";
  },
};
const headChildren: unknown[] = [];

(globalThis as unknown as { document: unknown }).document = {
  documentElement: {
    setAttribute(k: string, v: string) {
      attrs.set(k, v);
    },
    removeAttribute(k: string) {
      attrs.delete(k);
    },
    style: {
      setProperty(k: string, v: string) {
        styleProps.set(k, v);
      },
      removeProperty(k: string) {
        styleProps.delete(k);
      },
    },
  },
  head: {
    appendChild(el: unknown) {
      headChildren.push(el);
      styleEl.parentElement = {
        removeChild(child: unknown) {
          const idx = headChildren.indexOf(child);
          if (idx >= 0) headChildren.splice(idx, 1);
        },
      };
      return el;
    },
  },
  getElementById(id: string) {
    if (id === "reasonix-theme-pack-overlay" && headChildren.includes(styleEl)) return styleEl;
    return null;
  },
  createElement(tag: string) {
    if (tag === "style") return styleEl;
    return {};
  },
  querySelector() {
    return null;
  },
};

(globalThis as unknown as { window: unknown }).window = {
  matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }),
  runtime: undefined,
};

console.log("\ntheme pack contract");

ok(isSafeHex("#aabbcc"), "accepts #RRGGBB");
ok(isSafeHex("#aabbccdd"), "accepts #RRGGBBAA");
ok(!isSafeHex("url(x)"), "rejects url()");
ok(!isSafeHex("linear-gradient(red,blue)"), "rejects gradient");
ok(isThemeTokenKey("accent") && !isThemeTokenKey("hack"), "token whitelist");

ok(isSafeBackgroundURL("/__reasonix_theme_asset/my-theme/abc/background.png"), "asset URL allowed");
ok(isSafeBackgroundURL("data:image/png;base64,aaa"), "data URL allowed");
ok(!isSafeBackgroundURL("https://evil.example/bg.png"), "remote URL rejected");

const draft = draftPackView({
  id: "preview-pack",
  name: "Preview",
  baseStyle: "graphite",
  tokens: { dark: { accent: "#ff0000", fg: "#ffffff" }, light: { accent: "#0000ff" } },
  recipes: { density: "compact", corners: "round" },
  background: {
    focusX: 0.2,
    focusY: 0.8,
    safeArea: "left",
    homeOpacity: 1,
    taskOpacity: 0.2,
    overlayStrength: 0.5,
  },
  backgroundUrl: "/__reasonix_theme_asset/preview-pack/deadbeef/background.png",
});

applyThemePack(draft);
ok(attrs.get("data-theme-pack") === "preview-pack", "sets data-theme-pack");
ok(attrs.get("data-theme-has-bg") === "true", "marks background present");
ok(styleProps.has("--theme-bg-image"), "sets background image var");
ok((styleEl as { textContent: string }).textContent.includes("--accent:#ff0000"), "injects dark accent override");
ok((styleEl as { textContent: string }).textContent.includes("--r:14px"), "applies round corners recipe");

applyThemeScene("task");
ok(attrs.get("data-theme-scene") === "task", "scene task on root");

applyThemeScene("home");
ok(attrs.get("data-theme-scene") === "home", "scene home on root");

// Preview cancel restores previous (null) pack
clearThemePack();
beginThemePreview(draft);
ok(attrs.get("data-theme-pack") === "preview-pack", "preview applies pack");
cancelThemePreview();
ok(!attrs.has("data-theme-pack"), "cancel restores cleared pack");

// Restore-default must restore config baseStyle, not leave pack baseStyle.
setBaseAppearance("dark", "graphite");
applyTheme("dark", "graphite", { persist: false });
const aurora = draftPackView({
  id: "aurora",
  name: "Aurora",
  baseStyle: "aurora",
  tokens: {},
  recipes: { density: "comfortable", corners: "soft" },
});
applyThemePack(aurora);
ok(attrs.get("data-theme-pack") === "aurora", "aurora pack active");
ok(getThemeStyle() === "aurora", "pack switches live style to aurora");
clearThemePack();
ok(!attrs.has("data-theme-pack"), "clear removes data-theme-pack");
ok(getThemeStyle() === "graphite", "clear restores config graphite style");

// Density recipe must land in overlay CSS and have stylesheet consumers.
ok((styleEl as { textContent: string }).textContent.includes("--theme-density-pad") || packSource.includes("--theme-density-pad:6px"), "compact density vars defined in pack builder");
const compactDraft = draftPackView({
  id: "dense",
  name: "Dense",
  baseStyle: "graphite",
  tokens: {},
  recipes: { density: "compact", corners: "soft" },
});
applyThemePack(compactDraft);
ok((styleEl as { textContent: string }).textContent.includes("--theme-density-pad:6px"), "compact density injected");
ok((styleEl as { textContent: string }).textContent.includes("--theme-row-h:28px"), "compact row height injected");
ok(stylesSource.includes("padding: var(--theme-density-pad"), "density pad consumed by cards");
ok(stylesSource.includes("gap: var(--theme-density-gap"), "density gap consumed");
ok(stylesSource.includes("--list-row-height: var(--theme-row-h)"), "density maps to list row height");

// Layout must go transparent when a background is active so theme-bg is visible.
ok(
  stylesSource.includes(':root[data-theme-has-bg="true"] .layout') &&
    /data-theme-has-bg="true"\]\s*\.layout\s*\{[^}]*background:\s*transparent/s.test(stylesSource),
  "layout background transparent when theme has background",
);

// Unmount must cancel preview.
ok(librarySource.includes("cancelThemePreview()"), "ThemeLibrary cleanup cancels preview");

// Import confirm reuses staged import (replace=true empty path).
ok(
  librarySource.includes("ImportThemePack(\"\", true)") || librarySource.includes("ImportThemePack('', true)"),
  "import confirm reuses staged path without re-picking",
);
ok(librarySource.includes("needsReplace"), "import handles needsReplace result");

// Source contracts
ok(packSource.includes("reasonix-theme-pack-overlay"), "overlay style id stable");
ok(packSource.includes("appendChild(el)"), "overlay style appended last for priority");
ok(packSource.includes("baseAppearance"), "tracks base appearance for restore");
ok(stylesSource.includes(".theme-bg"), "background layer CSS present");
ok(stylesSource.includes("data-theme-scene=\"task\""), "task scene CSS present");
// Theme pack section must not *apply* backdrop-filter (comments may mention it).
const themeBgIdx = stylesSource.indexOf("Theme Pack V1");
const themeBgSlice = themeBgIdx >= 0 ? stylesSource.slice(themeBgIdx) : "";
ok(
  !/^\s*backdrop-filter\s*:/m.test(themeBgSlice) && !/^\s*-webkit-backdrop-filter\s*:/m.test(themeBgSlice),
  "theme pack CSS does not apply backdrop-filter",
);
ok(themeBgSlice.includes(".theme-bg__overlay"), "overlay wash element styled");
ok(appSource.includes("applyThemeScene"), "App wires scene from session content");
ok(appSource.includes("ThemeBackground"), "App mounts background layer");
ok(appSource.includes("setBaseAppearance"), "App seeds base appearance from config");
ok(appSource.includes("ResetThemePack") || appSource.includes("theme reset") || appSource.includes('arg === "reset"'), "reset entry exists");

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
