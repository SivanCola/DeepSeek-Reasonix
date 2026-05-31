import type { Balance, Settings, UsageStats } from "../v1/compat-types";
import type {
  FontFamily,
  FontScale,
  Theme,
  ThemeStyle,
} from "../v1/theme";
import type { ImportedMcpServer, McpSpecInfo, SettingsPatch } from "../v1/protocol";
import { SettingsModal, type PageId } from "../v1/ui/settings";

export function SettingsPanel({
  settings,
  balance,
  usage,
  currency,
  theme,
  themeStyle,
  onSetTheme,
  onSetThemeStyle,
  fontScale,
  onSetFontScale,
  fontFamily,
  onSetFontFamily,
  customFontFamily,
  onSetCustomFontFamily,
  initialPage,
  mcpSpecs,
  mcpBridged,
  onClose,
  onSave,
  onSaveApiKey,
  onImportCcSwitchMcp,
  onAddMcpSpec,
  onRemoveMcpSpec,
  onUpdateMcpSpec,
  onRetryMcpSpec,
}: {
  settings: Settings;
  balance: Balance | null;
  usage: UsageStats;
  currency: "CNY" | "USD";
  theme: Theme;
  themeStyle: ThemeStyle;
  onSetTheme: (theme: Theme) => void;
  onSetThemeStyle: (style: ThemeStyle) => void;
  fontScale: FontScale;
  onSetFontScale: (scale: FontScale) => void;
  fontFamily: FontFamily;
  onSetFontFamily: (family: FontFamily) => void;
  customFontFamily: string;
  onSetCustomFontFamily: (family: string) => void;
  initialPage?: PageId;
  mcpSpecs?: McpSpecInfo[];
  mcpBridged?: boolean;
  onClose: () => void;
  onSave: (patch: SettingsPatch) => void;
  onSaveApiKey: (key: string) => void;
  onImportCcSwitchMcp?: () => Promise<void>;
  onAddMcpSpec?: (spec: string) => void;
  onRemoveMcpSpec?: (spec: string) => void;
  onUpdateMcpSpec?: (raw: string, server: ImportedMcpServer) => void;
  onRetryMcpSpec?: (raw: string) => void;
}) {
  return (
    <SettingsModal
      settings={settings}
      balance={balance}
      usage={usage}
      currency={currency}
      theme={theme}
      themeStyle={themeStyle}
      onSetTheme={onSetTheme}
      onSetThemeStyle={onSetThemeStyle}
      fontScale={fontScale}
      onSetFontScale={onSetFontScale}
      fontFamily={fontFamily}
      onSetFontFamily={onSetFontFamily}
      customFontFamily={customFontFamily}
      onSetCustomFontFamily={onSetCustomFontFamily}
      initialPage={initialPage}
      mcpSpecs={mcpSpecs ?? []}
      mcpBridged={mcpBridged ?? false}
      skills={[]}
      memory={[]}
      memoryDetail={null}
      onClose={onClose}
      onSave={onSave}
      onSaveApiKey={onSaveApiKey}
      onImportCcSwitchMcp={onImportCcSwitchMcp ?? (async () => undefined)}
      onAddMcpSpec={onAddMcpSpec ?? (() => undefined)}
      onRemoveMcpSpec={onRemoveMcpSpec ?? (() => undefined)}
      onUpdateMcpSpec={onUpdateMcpSpec ?? (() => undefined)}
      onRetryMcpSpec={onRetryMcpSpec ?? (() => undefined)}
      onReadMemory={() => undefined}
    />
  );
}
