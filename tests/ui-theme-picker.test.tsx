import { render } from "ink";
import React from "react";
import { afterEach, describe, expect, it } from "vitest";
import { ThemePicker } from "../src/cli/ui/ThemePicker.js";
import type { ThemeChoice } from "../src/cli/ui/theme/labels.js";
import { type ThemeName, listThemeNames } from "../src/cli/ui/theme/tokens.js";
import { setLanguageRuntime } from "../src/i18n/index.js";
import { makeFakeStdin, makeFakeStdout } from "./helpers/ink-stdio.js";

function renderPicker(props: {
  currentPreference: ThemeChoice;
  activeTheme: ThemeName;
}): string {
  const stdout = makeFakeStdout();
  const { unmount } = render(
    React.createElement(ThemePicker, {
      currentPreference: props.currentPreference,
      activeTheme: props.activeTheme,
      onChoose: () => {},
    }),
    { stdout: stdout as never, stdin: makeFakeStdin() as never },
  );
  unmount();
  return stdout.text();
}

describe("ThemePicker", () => {
  afterEach(() => setLanguageRuntime("EN"));

  it("lists auto and all registered themes", () => {
    const text = renderPicker({ currentPreference: "auto", activeTheme: "graphite" });
    expect(text).toContain("auto");
    for (const name of listThemeNames()) {
      expect(text).toContain(name);
    }
  });

  it("marks the current preference and active theme", () => {
    const text = renderPicker({ currentPreference: "auto", activeTheme: "graphite" });
    expect(text).toMatch(/auto[\s\S]*current preference/);
    expect(text).toMatch(/graphite[\s\S]*active now/);
  });

  it("localizes labels while keeping the same theme ids", () => {
    setLanguageRuntime("zh-CN");
    const text = renderPicker({ currentPreference: "aurora", activeTheme: "aurora" });
    expect(text).toContain("极光 (aurora)");
    for (const name of listThemeNames()) {
      expect(text).toContain(`(${name})`);
    }
  });

  it("renders the keybind hint footer", () => {
    const text = renderPicker({ currentPreference: "midnight", activeTheme: "midnight" });
    expect(text).toContain("↑↓");
    expect(text).toContain("⏎");
    expect(text).toContain("esc");
  });
});
