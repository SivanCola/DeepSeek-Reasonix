// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import React from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Settings } from "../desktop/src/App";
import { setLang } from "../desktop/src/i18n";
import type { SettingsPatch } from "../desktop/src/protocol";
import type { QQDesktopSettingsState } from "../desktop/src/qq-settings";
import type { Theme, ThemeStyle } from "../desktop/src/theme";
import { SettingsModal } from "../desktop/src/ui/settings";

afterEach(() => {
  cleanup();
});

beforeEach(() => {
  setLang("en");
});

const BASE_SETTINGS: Settings = {
  reasoningEffort: "high",
  editMode: "review",
  budgetUsd: null,
  baseUrl: "",
  apiKeyPrefix: "sk-test",
  workspaceDir: "/tmp/reasonix",
  recentWorkspaces: [],
  model: "deepseek-v4-flash",
  editor: "cursor --goto",
  desktopCloseBehavior: "closeToQuit",
  webSearchEngine: "perplexity",
  webSearchApiKeys: {
    perplexity: "pplx-E...wkq",
  },
  subagentModels: {},
  contextTokens: {},
  showSystemEvents: true,
  version: "test",
};

const CONNECTED_QQ: QQDesktopSettingsState = {
  appId: "1903260000",
  appSecret: "secret",
  sandbox: false,
  enabled: true,
  configured: true,
  runtimeState: "connected",
  access: "open (unbound)",
};

function renderSettingsModal({
  settings = BASE_SETTINGS,
  qq = null,
  theme = "light",
  themeStyle = "sandstone",
  onClose = vi.fn(),
  onSave = vi.fn(),
}: {
  settings?: Settings;
  qq?: QQDesktopSettingsState | null;
  theme?: Theme;
  themeStyle?: ThemeStyle;
  onClose?: () => void;
  onSave?: (patch: SettingsPatch) => void;
} = {}) {
  const result = render(
    <SettingsModal
      settings={settings}
      balance={null}
      usage={{
        totalCostUsd: 0,
        totalPromptTokens: 0,
        totalCompletionTokens: 0,
        cacheHitTokens: 0,
        cacheMissTokens: 0,
        lastCallCacheHit: null,
        lastCallCacheMiss: null,
        reservedTokens: 0,
      }}
      currency="CNY"
      theme={theme}
      themeStyle={themeStyle}
      onSetTheme={vi.fn()}
      onSetThemeStyle={vi.fn()}
      fontScale="medium"
      onSetFontScale={vi.fn()}
      fontFamily="sans"
      onSetFontFamily={vi.fn()}
      customFontFamily=""
      onSetCustomFontFamily={vi.fn()}
      initialPage="general"
      mcpSpecs={[]}
      mcpBridged={false}
      skills={[]}
      memory={[]}
      memoryDetail={null}
      qq={qq}
      onClose={onClose}
      onSave={onSave}
      onSaveApiKey={vi.fn()}
      onLoadQQ={vi.fn()}
      onConnectQQ={vi.fn()}
      onDisconnectQQ={vi.fn()}
      onSaveQQConfig={vi.fn()}
      onOpenQQApplyLink={vi.fn()}
      onCheckForUpdates={vi.fn()}
      isCheckingForUpdates={false}
      hasUpdateAvailable={false}
      onPickWorkspace={vi.fn()}
      onImportCcSwitchMcp={vi.fn().mockResolvedValue(undefined)}
      onAddMcpSpec={vi.fn()}
      onRemoveMcpSpec={vi.fn()}
      onUpdateMcpSpec={vi.fn()}
      onRetryMcpSpec={vi.fn()}
      onReadMemory={vi.fn()}
    />,
  );
  return { ...result, onClose, onSave };
}

describe("desktop settings UI", () => {
  it("keeps reasoning effort only on the Models page", () => {
    renderSettingsModal();

    expect(screen.queryByText("Reasoning effort")).toBeNull();
    expect(screen.getByText("Edit gate")).toBeTruthy();
  });

  it("renders only the active mode theme styles in the combined theme setting", () => {
    const { container } = renderSettingsModal();

    expect(container.querySelectorAll(".style-card")).toHaveLength(4);
    expect(container.querySelectorAll('.style-grid[data-mode="light"] .style-card')).toHaveLength(
      4,
    );
    expect(container.querySelectorAll('.style-card[data-mode="dark"]')).toHaveLength(0);
  });

  it("covers the dark theme style set when dark mode is active", () => {
    const { container } = renderSettingsModal({
      theme: "dark",
      themeStyle: "graphite",
    });

    expect(container.querySelectorAll(".style-card")).toHaveLength(4);
    expect(container.querySelectorAll('.style-grid[data-mode="dark"] .style-card')).toHaveLength(4);
    expect(container.querySelectorAll('.style-card[data-mode="light"]')).toHaveLength(0);
  });

  it("lets Escape close the modal from focused controls", () => {
    const { onClose } = renderSettingsModal();

    fireEvent.keyDown(screen.getByRole("button", { name: "light" }), { key: "Escape" });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("keeps web search API key actions grouped without status-as-button controls", () => {
    renderSettingsModal();

    const row = screen.getByText("Perplexity API key").closest(".setting-row");
    expect(row?.querySelector(".credential-row-control")).toBeTruthy();
    expect(row?.querySelector(".credential-primary-line")).toBeTruthy();
    expect(row?.querySelector('[data-testid="connection-status-webSearch"]')).toBeNull();
    expect(row?.querySelectorAll(".credential-row-control .btn.primary")).toHaveLength(1);
  });

  it("shows QQ connection state as a badge without unbound wording conflict", () => {
    renderSettingsModal({ qq: CONNECTED_QQ });

    const status = screen.getByTestId("qq-status-badge");
    expect(status.textContent).toBe("Connected");
    expect(status.closest("button")).toBeNull();
    expect(screen.queryByText(/unbound/i)).toBeNull();
    expect(screen.getByText(/Open access/)).toBeTruthy();
  });
});
