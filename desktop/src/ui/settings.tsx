import { openUrl } from "@tauri-apps/plugin-opener";
import { type ReactNode, useEffect, useRef, useState } from "react";
import type { Balance, Settings as SettingsType, UsageStats } from "../App";
import { getLangLabel, getSupportedLangs, setLang, t, useLang } from "../i18n";
import { I } from "../icons";
import type {
  ImportedMcpServer,
  McpSpecInfo,
  MemoryDetail,
  MemoryEntryInfo,
  SettingsPatch,
  SkillInfo,
} from "../protocol";
import {
  type QQDesktopSettingsState,
  describeQQRowSummary,
  getQQConnectIntent,
  getQQStatusLabel,
} from "../qq-settings";
import {
  FONT_FAMILY,
  FONT_SCALE,
  type FontFamily,
  type FontScale,
  THEME,
  THEME_STYLES,
  type Theme,
  type ThemeStyle,
  themeForStyle,
} from "../theme";
import { McpServerCard } from "./mcp-server-card";
import { Shortcut, type ShortcutKey } from "./shortcut";

export type PageId =
  | "general"
  | "models"
  | "mcp"
  | "skills"
  | "memory"
  | "rules"
  | "billing"
  | "shortcuts";

const PAGE_META: ReadonlyArray<{ id: PageId; icon: keyof typeof I }> = [
  { id: "general", icon: "cog" },
  { id: "models", icon: "brain" },
  { id: "mcp", icon: "wrench" },
  { id: "skills", icon: "zap" },
  { id: "memory", icon: "bookmark" },
  { id: "rules", icon: "shield" },
  { id: "billing", icon: "coin" },
  { id: "shortcuts", icon: "cpu" },
];

const DARK_THEME_STYLES = THEME_STYLES.filter((style) => themeForStyle(style) === THEME.DARK);
const LIGHT_THEME_STYLES = THEME_STYLES.filter((style) => themeForStyle(style) === THEME.LIGHT);
const EFFORT_VALUES = ["low", "medium", "high", "max"] as const;
type EffortValue = (typeof EFFORT_VALUES)[number];
type SettingsStatusTone = "neutral" | "success" | "danger" | "warning" | "info";

function SettingsSection({
  title,
  className,
  children,
}: {
  title: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <section className={className ? `section ${className}` : "section"}>
      <div className="stitle">{title}</div>
      {children}
    </section>
  );
}

function SettingsRow({
  label,
  hint,
  className,
  controlClassName,
  children,
}: {
  label: ReactNode;
  hint?: ReactNode;
  className?: string;
  controlClassName?: string;
  children: ReactNode;
}) {
  return (
    <div className={className ? `setting-row ${className}` : "setting-row"}>
      <div className="settings-row-label">
        <div className="n">{label}</div>
        {hint ? <div className="h">{hint}</div> : null}
      </div>
      <div
        className={
          controlClassName ? `settings-control-group ${controlClassName}` : "settings-control-group"
        }
      >
        {children}
      </div>
    </div>
  );
}

function SettingsControlGroup({
  className,
  children,
}: {
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={className ? `settings-control-group ${className}` : "settings-control-group"}>
      {children}
    </div>
  );
}

function SettingsStatusBadge({
  tone = "neutral",
  title,
  children,
  testId,
}: {
  tone?: SettingsStatusTone;
  title?: string;
  children: ReactNode;
  testId?: string;
}) {
  return (
    <span className="settings-status-badge" data-tone={tone} title={title} data-testid={testId}>
      {children}
    </span>
  );
}

function SettingsEmptyState({ children }: { children: ReactNode }) {
  return <div className="settings-empty-state">{children}</div>;
}

function ThemeStyleCard({
  style,
  selected,
  onSelect,
}: {
  style: ThemeStyle;
  selected: boolean;
  onSelect: (style: ThemeStyle) => void;
}) {
  return (
    <button
      key={style}
      type="button"
      className="style-card"
      data-on={selected}
      data-style={style}
      data-mode={themeForStyle(style)}
      onClick={() => onSelect(style)}
    >
      <span className="style-card-head">
        <span className="style-name">
          {t(`settings.themeStyle${style[0]!.toUpperCase()}${style.slice(1)}` as any)}
        </span>
        <span className="style-mode">
          {themeForStyle(style) === THEME.DARK ? t("settings.themeDark") : t("settings.themeLight")}
        </span>
      </span>
      <span className="style-swatches" aria-hidden="true">
        <span />
        <span />
        <span />
      </span>
      <span className="style-desc">
        {t(`settings.themeStyle${style[0]!.toUpperCase()}${style.slice(1)}Desc` as any)}
      </span>
    </button>
  );
}

export function SettingsModal({
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
  initialMcpEditRaw,
  initialMcpEditNonce,
  mcpSpecs,
  mcpBridged,
  skills,
  memory,
  memoryDetail,
  qq,
  onClose,
  onSave,
  onSaveApiKey,
  onLoadQQ,
  onConnectQQ,
  onDisconnectQQ,
  onSaveQQConfig,
  onOpenQQApplyLink,
  onPickWorkspace,
  onImportCcSwitchMcp,
  onAddMcpSpec,
  onRemoveMcpSpec,
  onUpdateMcpSpec,
  onRetryMcpSpec,
  onReadMemory,
}: {
  settings: SettingsType;
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
  initialMcpEditRaw?: string | null;
  initialMcpEditNonce?: number;
  mcpSpecs: McpSpecInfo[];
  mcpBridged: boolean;
  skills: SkillInfo[];
  memory: MemoryEntryInfo[];
  memoryDetail: MemoryDetail | null;
  qq: QQDesktopSettingsState | null;
  onClose: () => void;
  onSave: (patch: SettingsPatch) => void;
  onSaveApiKey: (key: string) => void;
  onLoadQQ: () => void;
  onConnectQQ: () => void;
  onDisconnectQQ: () => void;
  onSaveQQConfig: (patch: { appId?: string; appSecret?: string; sandbox: boolean }) => void;
  onOpenQQApplyLink: () => void;
  onPickWorkspace: () => void;
  onImportCcSwitchMcp: () => Promise<void>;
  onAddMcpSpec: (spec: string) => void;
  onRemoveMcpSpec: (spec: string) => void;
  onUpdateMcpSpec: (raw: string, server: ImportedMcpServer) => void;
  onRetryMcpSpec: (raw: string) => void;
  onReadMemory: (path: string) => void;
}) {
  const [page, setPage] = useState<PageId>(initialPage ?? "general");
  const [qqConfigureOpen, setQQConfigureOpen] = useState(false);
  useEffect(() => {
    if (initialPage) setPage(initialPage);
  }, [initialPage]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  const currentMeta = PAGE_META.find((p) => p.id === page) ?? PAGE_META[0]!;
  return (
    <div
      className="settings-mask"
      onClick={onClose}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") onClose();
      }}
    >
      <div
        className="settings"
        onClick={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") e.stopPropagation();
        }}
      >
        <nav className="settings-side">
          <div className="sg">{t("settings.title")}</div>
          {PAGE_META.map((p) => (
            <button
              type="button"
              key={p.id}
              className="row"
              data-active={page === p.id}
              onClick={() => setPage(p.id)}
            >
              <span className="ico">{I[p.icon]({ size: 13 })}</span>
              <span>{t(`settings.page${p.id[0]!.toUpperCase()}${p.id.slice(1)}Label` as any)}</span>
            </button>
          ))}
        </nav>
        <div className="settings-main">
          <div className="settings-head">
            <div>
              <h2>
                {t(
                  `settings.page${currentMeta.id[0]!.toUpperCase()}${currentMeta.id.slice(1)}Label` as any,
                )}
              </h2>
              <div className="desc">
                {t(
                  `settings.page${currentMeta.id[0]!.toUpperCase()}${currentMeta.id.slice(1)}Desc` as any,
                )}
              </div>
            </div>
            <span className="grow" />
            <button type="button" className="close-btn" onClick={onClose}>
              <I.x size={14} />
            </button>
          </div>
          <div className="settings-body">
            {page === "general" && (
              <PageGeneral
                settings={settings}
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
                onSave={onSave}
                onPickWorkspace={onPickWorkspace}
              />
            )}
            {page === "models" && <PageModels settings={settings} onSave={onSave} />}
            {page === "mcp" && (
              <PageMCP
                specs={mcpSpecs}
                bridged={mcpBridged}
                initialEditRaw={initialMcpEditRaw}
                initialEditNonce={initialMcpEditNonce}
                onImportCcSwitch={onImportCcSwitchMcp}
                onAdd={onAddMcpSpec}
                onRemove={onRemoveMcpSpec}
                onUpdate={onUpdateMcpSpec}
                onRetry={onRetryMcpSpec}
              />
            )}
            {page === "skills" && (
              <PageSkills
                skills={skills}
                subagentModels={settings.subagentModels ?? {}}
                onSave={onSave}
              />
            )}
            {page === "memory" && (
              <PageMemory entries={memory} detail={memoryDetail} onRead={onReadMemory} />
            )}
            {page === "rules" && <PageRules settings={settings} onSave={onSave} />}
            {page === "billing" && (
              <PageBilling balance={balance} usage={usage} currency={currency} />
            )}
            {page === "shortcuts" && <PageShortcuts />}
            {page === "general" ? (
              <>
                <ApiKeySection
                  baseUrl={settings.baseUrl}
                  apiKeyPrefix={settings.apiKeyPrefix}
                  onSave={onSave}
                  onSaveApiKey={onSaveApiKey}
                />
                <QQChannelSection
                  qq={qq}
                  configureOpen={qqConfigureOpen}
                  onOpenConfigure={() => {
                    onLoadQQ();
                    setQQConfigureOpen(true);
                  }}
                  onCloseConfigure={() => setQQConfigureOpen(false)}
                  onConnect={onConnectQQ}
                  onDisconnect={onDisconnectQQ}
                  onSaveConfig={onSaveQQConfig}
                  onSaveAndConnect={(patch) => {
                    onSaveQQConfig(patch);
                    onConnectQQ();
                  }}
                  onOpenApplyLink={onOpenQQApplyLink}
                />
              </>
            ) : null}
          </div>
        </div>
      </div>
    </div>
  );
}

export function QQChannelSection({
  qq,
  configureOpen,
  onOpenConfigure,
  onCloseConfigure,
  onConnect,
  onDisconnect,
  onSaveConfig,
  onSaveAndConnect,
  onOpenApplyLink,
}: {
  qq: QQDesktopSettingsState | null;
  configureOpen: boolean;
  onOpenConfigure: () => void;
  onCloseConfigure: () => void;
  onConnect: () => void;
  onDisconnect: () => void;
  onSaveConfig: (patch: { appId?: string; appSecret?: string; sandbox: boolean }) => void;
  onSaveAndConnect: (patch: { appId?: string; appSecret?: string; sandbox: boolean }) => void;
  onOpenApplyLink: () => void;
}) {
  const current = qq ?? {
    appId: undefined,
    appSecret: undefined,
    sandbox: true,
    enabled: false,
    configured: false,
    runtimeState: "disconnected",
    access: "open (unbound)",
  };
  const [appId, setAppId] = useState(current.appId ?? "");
  const [appSecret, setAppSecret] = useState(current.appSecret ?? "");
  const [sandbox, setSandbox] = useState(current.sandbox ?? true);

  useEffect(() => {
    setAppId(current.appId ?? "");
    setAppSecret(current.appSecret ?? "");
    setSandbox(current.sandbox ?? true);
  }, [current.appId, current.appSecret, current.sandbox]);

  const savePatch = { appId, appSecret, sandbox };

  const statusTone: SettingsStatusTone =
    current.runtimeState === "connected"
      ? "success"
      : current.runtimeState === "failed"
        ? "danger"
        : current.runtimeState === "connecting"
          ? "info"
          : "neutral";
  const connectIntent = getQQConnectIntent(current);
  return (
    <SettingsSection title={t("settings.qqSection")}>
      {!configureOpen ? (
        <SettingsRow
          label={t("settings.qqTitle")}
          hint={describeQQRowSummary(current)}
          className="qq-setting-row"
          controlClassName="qq-row-actions"
        >
          <SettingsStatusBadge tone={statusTone} testId="qq-status-badge">
            {getQQStatusLabel(current)}
          </SettingsStatusBadge>
          {current.runtimeState === "connected" ? (
            <button type="button" className="btn danger" onClick={onDisconnect}>
              {t("settings.qqDisconnect")}
            </button>
          ) : (
            <button
              type="button"
              className="btn"
              onClick={connectIntent === "configure" ? onOpenConfigure : onConnect}
            >
              {connectIntent === "configure" ? t("settings.qqConfigure") : t("settings.qqConnect")}
            </button>
          )}
          {connectIntent !== "configure" ? (
            <button type="button" className="btn" onClick={onOpenConfigure}>
              {t("settings.qqConfigure")}
            </button>
          ) : null}
        </SettingsRow>
      ) : (
        <div className="qq-config-card">
          <div className="qq-config-head">
            <div className="settings-row-label">
              <div className="n">{t("settings.qqConfigureTitle")}</div>
              <div className="h">{t("settings.qqConfigureHint")}</div>
            </div>
            <button type="button" className="btn" onClick={onCloseConfigure}>
              {t("settings.qqBack")}
            </button>
          </div>
          <SettingsRow label={t("settings.qqAppId")}>
            <input
              className="field mono"
              value={appId}
              onChange={(e) => setAppId(e.target.value)}
              placeholder="QQ Open Platform App ID"
            />
          </SettingsRow>
          <SettingsRow label={t("settings.qqAppSecret")}>
            <input
              className="field mono"
              type="password"
              value={appSecret}
              onChange={(e) => setAppSecret(e.target.value)}
              placeholder="QQ Open Platform App Secret"
            />
          </SettingsRow>
          <SettingsRow label={t("settings.qqEnvironment")}>
            <div className="seg-ctrl">
              <button type="button" data-on={sandbox} onClick={() => setSandbox(true)}>
                {t("settings.qqSandbox")}
              </button>
              <button type="button" data-on={!sandbox} onClick={() => setSandbox(false)}>
                {t("settings.qqProduction")}
              </button>
            </div>
          </SettingsRow>
          <SettingsRow label={t("settings.qqApplyLabel")}>
            <button type="button" className="btn" onClick={onOpenApplyLink}>
              {t("settings.qqApplyAction")}
            </button>
          </SettingsRow>
          <div className="qq-config-actions">
            <button
              type="button"
              className="btn"
              onClick={() => {
                onSaveConfig(savePatch);
                onCloseConfigure();
              }}
            >
              {t("settings.qqSave")}
            </button>
            <button
              type="button"
              className="btn primary"
              onClick={() => {
                onSaveAndConnect(savePatch);
                onCloseConfigure();
              }}
            >
              {t("settings.qqSaveAndConnect")}
            </button>
          </div>
        </div>
      )}
    </SettingsSection>
  );
}

function PageGeneral({
  settings,
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
  onSave,
  onPickWorkspace,
}: {
  settings: SettingsType;
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
  onSave: (patch: SettingsPatch) => void;
  onPickWorkspace: () => void;
}) {
  const [editorDraft, setEditorDraft] = useState(settings.editor ?? "");
  const [customFontDraft, setCustomFontDraft] = useState(customFontFamily);
  const [budgetDraft, setBudgetDraft] = useState(
    settings.budgetUsd == null ? "" : String(settings.budgetUsd),
  );
  const lang = useLang();
  useEffect(() => {
    setEditorDraft(settings.editor ?? "");
  }, [settings.editor]);
  useEffect(() => {
    setCustomFontDraft(customFontFamily);
  }, [customFontFamily]);
  useEffect(() => {
    setBudgetDraft(settings.budgetUsd == null ? "" : String(settings.budgetUsd));
  }, [settings.budgetUsd]);
  const commitCustomFont = (value: string) => {
    const next = value.trim();
    setCustomFontDraft(next);
    onSetCustomFontFamily(next);
  };
  const commitBudget = () => {
    const v = budgetDraft.trim();
    onSave({ budgetUsd: v === "" ? null : Number(v) });
  };
  const visibleThemeStyles = theme === THEME.DARK ? DARK_THEME_STYLES : LIGHT_THEME_STYLES;
  return (
    <>
      <SettingsSection title={t("settings.appearanceSection")}>
        <SettingsRow
          label={t("settings.theme")}
          hint={t("settings.themeHint")}
          className="theme-style-row"
          controlClassName="theme-style-control theme-combo-control"
        >
          <div className="theme-mode-bar">
            <div className="seg-ctrl">
              <button
                type="button"
                data-on={theme === THEME.DARK}
                onClick={() => onSetTheme(THEME.DARK)}
              >
                {t("settings.themeDark")}
              </button>
              <button
                type="button"
                data-on={theme === THEME.LIGHT}
                onClick={() => onSetTheme(THEME.LIGHT)}
              >
                {t("settings.themeLight")}
              </button>
            </div>
            <span className="theme-style-caption">{t("settings.themeStyle")}</span>
          </div>
          <div className="style-grid" data-mode={theme}>
            {visibleThemeStyles.map((style) => (
              <ThemeStyleCard
                key={style}
                style={style}
                selected={themeStyle === style}
                onSelect={onSetThemeStyle}
              />
            ))}
          </div>
        </SettingsRow>
        <SettingsRow label={t("settings.fontScale")} hint={t("settings.fontScaleHint")}>
          <div className="seg-ctrl">
            <button
              type="button"
              data-on={fontScale === FONT_SCALE.SMALL}
              onClick={() => onSetFontScale(FONT_SCALE.SMALL)}
            >
              {t("settings.fontScaleSmall")}
            </button>
            <button
              type="button"
              data-on={fontScale === FONT_SCALE.MEDIUM}
              onClick={() => onSetFontScale(FONT_SCALE.MEDIUM)}
            >
              {t("settings.fontScaleMedium")}
            </button>
            <button
              type="button"
              data-on={fontScale === FONT_SCALE.LARGE}
              onClick={() => onSetFontScale(FONT_SCALE.LARGE)}
            >
              {t("settings.fontScaleLarge")}
            </button>
          </div>
        </SettingsRow>
        <SettingsRow label={t("settings.fontFamily")} hint={t("settings.fontFamilyHint")}>
          <div className="seg-ctrl">
            <button
              type="button"
              data-on={fontFamily === FONT_FAMILY.SANS}
              onClick={() => onSetFontFamily(FONT_FAMILY.SANS)}
            >
              {t("settings.fontFamilySans")}
            </button>
            <button
              type="button"
              data-on={fontFamily === FONT_FAMILY.SYSTEM}
              onClick={() => onSetFontFamily(FONT_FAMILY.SYSTEM)}
            >
              {t("settings.fontFamilySystem")}
            </button>
            <button
              type="button"
              data-on={fontFamily === FONT_FAMILY.SERIF}
              onClick={() => onSetFontFamily(FONT_FAMILY.SERIF)}
            >
              {t("settings.fontFamilySerif")}
            </button>
            <button
              type="button"
              data-on={fontFamily === FONT_FAMILY.CUSTOM}
              onClick={() => onSetFontFamily(FONT_FAMILY.CUSTOM)}
            >
              {t("settings.fontFamilyCustom")}
            </button>
          </div>
        </SettingsRow>
        {fontFamily === FONT_FAMILY.CUSTOM && (
          <SettingsRow
            label={t("settings.customFontFamily")}
            hint={t("settings.customFontFamilyHint")}
          >
            <input
              className="field font-family-field"
              value={customFontDraft}
              placeholder={`"Microsoft YaHei", "PingFang SC", sans-serif`}
              onChange={(e) => {
                setCustomFontDraft(e.target.value);
                onSetCustomFontFamily(e.target.value);
              }}
              onBlur={(e) => commitCustomFont(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.currentTarget.blur();
                }
              }}
            />
          </SettingsRow>
        )}
        <SettingsRow label={t("settings.language")} hint={t("settings.languageHint")}>
          <div className="seg-ctrl">
            {getSupportedLangs().map((code) => (
              <button
                type="button"
                key={code}
                data-on={lang === code}
                onClick={() => setLang(code)}
              >
                {getLangLabel(code)}
              </button>
            ))}
          </div>
        </SettingsRow>
      </SettingsSection>

      <SettingsSection title={t("settings.workspaceSection")}>
        <SettingsRow
          label={t("settings.currentWorkspace")}
          hint={settings.workspaceDir || t("settings.notSelected")}
        >
          <button type="button" className="btn" onClick={onPickWorkspace}>
            {t("settings.workspaceChange")}
          </button>
        </SettingsRow>
        <SettingsRow label={t("settings.editor")} hint={t("settings.editorHint")}>
          <input
            className="field mono"
            value={editorDraft}
            placeholder="cursor --goto"
            onChange={(e) => setEditorDraft(e.target.value)}
            onBlur={() => onSave({ editor: editorDraft.trim() })}
          />
        </SettingsRow>
      </SettingsSection>

      <SettingsSection title={t("settings.behaviorSection")}>
        <SettingsRow label={t("settings.editMode")} hint={t("settings.editModeHint")}>
          <div className="seg-ctrl">
            {(["plan", "review", "auto", "yolo"] as const).map((m) => (
              <button
                type="button"
                key={m}
                data-on={settings.editMode === m}
                onClick={() => onSave({ editMode: m })}
              >
                {m}
              </button>
            ))}
          </div>
        </SettingsRow>
        <SettingsRow
          label={t("settings.showSystemEvents")}
          hint={t("settings.showSystemEventsHint")}
        >
          <div className="seg-ctrl">
            <button
              type="button"
              data-on={settings.showSystemEvents !== false}
              onClick={() => onSave({ showSystemEvents: true })}
            >
              {t("settings.shown")}
            </button>
            <button
              type="button"
              data-on={settings.showSystemEvents === false}
              onClick={() => onSave({ showSystemEvents: false })}
            >
              {t("settings.hidden")}
            </button>
          </div>
        </SettingsRow>
        <SettingsRow
          label={t("settings.desktopCloseBehavior")}
          hint={t("settings.desktopCloseBehaviorHint")}
        >
          <div className="seg-ctrl">
            <button
              type="button"
              data-on={(settings.desktopCloseBehavior ?? "closeToQuit") === "closeToQuit"}
              onClick={() => onSave({ desktopCloseBehavior: "closeToQuit" })}
            >
              {t("settings.closeToQuit")}
            </button>
            <button
              type="button"
              data-on={settings.desktopCloseBehavior === "closeToTray"}
              onClick={() => onSave({ desktopCloseBehavior: "closeToTray" })}
            >
              {t("settings.closeToTray")}
            </button>
          </div>
        </SettingsRow>
        <SettingsRow label={t("settings.budget")} hint={t("settings.budgetHint")}>
          <SettingsControlGroup className="field-layout">
            <input
              className="field"
              type="number"
              min={0}
              value={budgetDraft}
              placeholder={t("settings.budgetPlaceholder")}
              onChange={(e) => setBudgetDraft(e.target.value)}
              onBlur={commitBudget}
              onKeyDown={(e) => {
                if (e.key === "Enter") e.currentTarget.blur();
              }}
            />
            <span className="field-suffix">USD</span>
          </SettingsControlGroup>
        </SettingsRow>
        <SettingsRow label={t("settings.webSearchEngine")} hint={t("settings.webSearchEngineNote")}>
          <select
            className="field"
            value={settings.webSearchEngine ?? "bing"}
            onChange={(e) =>
              onSave({
                webSearchEngine: e.target.value as
                  | "bing"
                  | "bing-intl"
                  | "searxng"
                  | "metaso"
                  | "baidu"
                  | "tavily"
                  | "perplexity"
                  | "exa"
                  | "brave"
                  | "ollama",
              })
            }
          >
            <option value="bing">{t("settings.webSearchEngineBing")}</option>
            <option value="bing-intl">{t("settings.webSearchEngineBingIntl")}</option>
            <option value="searxng">{t("settings.webSearchEngineSearxng")}</option>
            <option value="metaso">{t("settings.webSearchEngineMetaso")}</option>
            <option value="baidu">{t("settings.webSearchEngineBaidu")}</option>
            <option value="tavily">{t("settings.webSearchEngineTavily")}</option>
            <option value="perplexity">{t("settings.webSearchEnginePerplexity")}</option>
            <option value="exa">{t("settings.webSearchEngineExa")}</option>
            <option value="brave">{t("settings.webSearchEngineBrave")}</option>
            <option value="ollama">{t("settings.webSearchEngineOllama")}</option>
          </select>
        </SettingsRow>
        <WebSearchEngineCredentials settings={settings} onSave={onSave} />
      </SettingsSection>
    </>
  );
}

const SEARCH_ENGINE_API_KEY_FIELDS: ReadonlyArray<{
  engine: "metaso" | "baidu" | "tavily" | "perplexity" | "exa" | "brave" | "ollama";
  patchKey:
    | "metasoApiKey"
    | "baiduApiKey"
    | "tavilyApiKey"
    | "perplexityApiKey"
    | "exaApiKey"
    | "braveApiKey"
    | "ollamaApiKey";
  signupUrl: string;
}> = [
  { engine: "metaso", patchKey: "metasoApiKey", signupUrl: "https://metaso.cn/settings/api" },
  {
    engine: "baidu",
    patchKey: "baiduApiKey",
    signupUrl: "https://cloud.baidu.com/doc/qianfan/s/2mh4su4uy",
  },
  { engine: "tavily", patchKey: "tavilyApiKey", signupUrl: "https://app.tavily.com" },
  {
    engine: "perplexity",
    patchKey: "perplexityApiKey",
    signupUrl: "https://www.perplexity.ai/settings/api",
  },
  { engine: "exa", patchKey: "exaApiKey", signupUrl: "https://dashboard.exa.ai/api-keys" },
  { engine: "brave", patchKey: "braveApiKey", signupUrl: "https://brave.com/search/api/" },
  { engine: "ollama", patchKey: "ollamaApiKey", signupUrl: "https://ollama.com/settings/keys" },
];

function WebSearchEngineCredentials({
  settings,
  onSave,
}: {
  settings: SettingsType;
  onSave: (patch: SettingsPatch) => void;
}) {
  const engine = settings.webSearchEngine ?? "bing";
  if (engine === "bing" || engine === "bing-intl") return null;
  if (engine === "searxng") {
    return <SearxngEndpointRow settings={settings} onSave={onSave} />;
  }
  const field = SEARCH_ENGINE_API_KEY_FIELDS.find((f) => f.engine === engine);
  if (!field) return null;
  const prefix = settings.webSearchApiKeys?.[engine];
  return (
    <WebSearchApiKeyRow
      engine={engine}
      patchKey={field.patchKey}
      signupUrl={field.signupUrl}
      prefix={prefix}
      onSave={onSave}
    />
  );
}

function SearxngEndpointRow({
  settings,
  onSave,
}: {
  settings: SettingsType;
  onSave: (patch: SettingsPatch) => void;
}) {
  const [draft, setDraft] = useState(settings.webSearchEndpoint ?? "");
  useEffect(() => {
    setDraft(settings.webSearchEndpoint ?? "");
  }, [settings.webSearchEndpoint]);
  return (
    <SettingsRow
      label={t("settings.webSearchEndpoint")}
      hint={t("settings.webSearchEndpointHint")}
      controlClassName="credential-row-control"
    >
      <div className="credential-control">
        <div className="credential-primary-line">
          <input
            className="field mono"
            value={draft}
            placeholder="http://localhost:8080"
            onChange={(e) => setDraft(e.target.value)}
            onBlur={() => {
              const next = draft.trim();
              if (next === (settings.webSearchEndpoint ?? "")) return;
              onSave({ webSearchEndpoint: next || null });
            }}
          />
        </div>
      </div>
    </SettingsRow>
  );
}

function WebSearchApiKeyRow({
  engine,
  patchKey,
  signupUrl,
  prefix,
  onSave,
}: {
  engine: "metaso" | "baidu" | "tavily" | "perplexity" | "exa" | "brave" | "ollama";
  patchKey:
    | "metasoApiKey"
    | "baiduApiKey"
    | "tavilyApiKey"
    | "perplexityApiKey"
    | "exaApiKey"
    | "braveApiKey"
    | "ollamaApiKey";
  signupUrl: string;
  prefix?: string;
  onSave: (patch: SettingsPatch) => void;
}) {
  const [draft, setDraft] = useState("");
  const label = t(`settings.webSearchApiKey.${engine}` as const);
  return (
    <SettingsRow
      label={label}
      hint={
        <>
          {prefix ? t("settings.apiKeySet", { prefix }) : t("settings.apiKeyNotSet")}{" "}
          <a
            href={signupUrl}
            target="_blank"
            rel="noreferrer"
            onClick={(e) => {
              e.preventDefault();
              void openUrl(signupUrl).catch(() => undefined);
            }}
          >
            {t("settings.webSearchApiKeySignup")}
          </a>
        </>
      }
      controlClassName="credential-row-control"
    >
      <div className="credential-control">
        <div className="credential-primary-line">
          <input
            className="field mono"
            type="password"
            value={draft}
            placeholder={prefix ?? ""}
            onChange={(e) => setDraft(e.target.value)}
          />
          <button
            type="button"
            className="btn primary"
            disabled={!draft.trim()}
            onClick={() => {
              const trimmed = draft.trim();
              if (!trimmed) return;
              onSave({ [patchKey]: trimmed } as SettingsPatch);
              setDraft("");
            }}
          >
            {t("settings.apiKeySave")}
          </button>
          {prefix ? (
            <button
              type="button"
              className="btn"
              onClick={() => onSave({ [patchKey]: null } as SettingsPatch)}
            >
              {t("settings.webSearchApiKeyClear")}
            </button>
          ) : null}
        </div>
      </div>
    </SettingsRow>
  );
}

function ApiKeySection({
  baseUrl,
  apiKeyPrefix,
  onSave,
  onSaveApiKey,
}: {
  baseUrl?: string;
  apiKeyPrefix?: string;
  onSave: (patch: SettingsPatch) => void;
  onSaveApiKey: (key: string) => void;
}) {
  const [key, setKey] = useState("");
  const [urlDraft, setUrlDraft] = useState(baseUrl ?? "");
  useEffect(() => {
    setUrlDraft(baseUrl ?? "");
  }, [baseUrl]);
  return (
    <SettingsSection title={t("settings.apiSection")}>
      <SettingsRow
        label={t("settings.apiKey")}
        hint={
          apiKeyPrefix
            ? t("settings.apiKeySet", { prefix: apiKeyPrefix })
            : t("settings.apiKeyNotSet")
        }
        controlClassName="credential-row-control"
      >
        <div className="credential-control">
          <div className="credential-primary-line">
            <input
              className="field mono"
              type="password"
              value={key}
              onChange={(e) => setKey(e.target.value)}
              placeholder="sk-..."
            />
            <button
              type="button"
              className="btn primary"
              disabled={!key}
              onClick={() => {
                if (!key) return;
                onSaveApiKey(key);
                setKey("");
              }}
            >
              {t("settings.apiKeySave")}
            </button>
          </div>
        </div>
      </SettingsRow>
      <SettingsRow label={t("settings.baseUrl")} hint={t("settings.baseUrlHint")}>
        <input
          className="field mono"
          value={urlDraft}
          onChange={(e) => setUrlDraft(e.target.value)}
          onBlur={() => onSave({ baseUrl: urlDraft.trim() })}
        />
      </SettingsRow>
    </SettingsSection>
  );
}

const KNOWN_MODELS = ["deepseek-v4-flash", "deepseek-v4-pro"] as const;

function PageModels({
  settings,
  onSave,
}: {
  settings: SettingsType;
  onSave: (patch: SettingsPatch) => void;
}) {
  const [draft, setDraft] = useState(settings.model);
  useEffect(() => setDraft(settings.model), [settings.model]);
  const isKnown = (KNOWN_MODELS as readonly string[]).includes(settings.model);
  return (
    <>
      <SettingsSection title={t("settings.defaultModelCurrent", { model: settings.model })}>
        <div className="model-grid">
          {KNOWN_MODELS.map((id) => (
            <button
              type="button"
              key={id}
              className="mcard"
              data-on={settings.model === id}
              onClick={() => onSave({ model: id })}
            >
              <div className="nm">{id}</div>
            </button>
          ))}
        </div>
        <SettingsRow
          label={t("settings.modelCustom")}
          hint={t("settings.modelCustomHint")}
          className="settings-row-spaced"
        >
          <input
            className="field mono"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder="deepseek-v4-flash"
          />
          <button
            type="button"
            className="btn primary"
            disabled={!draft.trim() || draft.trim() === settings.model}
            onClick={() => onSave({ model: draft.trim() })}
          >
            {t("settings.apiKeySave")}
          </button>
        </SettingsRow>
        {!isKnown ? (
          <div className="settings-inline-note settings-note-block">
            {t("settings.modelCustomActive", { model: settings.model })}
          </div>
        ) : null}
        <SettingsRow
          label={t("settings.contextTokensLabel")}
          hint={t("settings.contextTokensHint")}
          className="settings-row-spaced"
        >
          <SettingsControlGroup className="field-layout">
            <input
              className="field mono"
              type="number"
              min={1}
              value={settings.contextTokens?.[settings.model] ?? ""}
              onChange={(e) => {
                const raw = e.target.value.trim();
                const num = raw ? Number.parseInt(raw, 10) : 0;
                const next = { ...(settings.contextTokens ?? {}) };
                if (num > 0 && Number.isFinite(num)) {
                  next[settings.model] = num;
                } else {
                  delete next[settings.model];
                }
                onSave({ contextTokens: Object.keys(next).length > 0 ? next : undefined });
              }}
              placeholder={t("settings.contextTokensPlaceholder")}
            />
            <span className="field-suffix">tokens</span>
          </SettingsControlGroup>
        </SettingsRow>
      </SettingsSection>
      <SettingsSection title={t("settings.effortSection")}>
        <SettingsRow label={t("settings.reasoningEffort")} hint={t("settings.reasoningEffortHint")}>
          <div className="seg-ctrl">
            {EFFORT_VALUES.map((e) => (
              <button
                type="button"
                key={e}
                data-on={settings.reasoningEffort === e}
                onClick={() => onSave({ reasoningEffort: e as EffortValue })}
              >
                {e}
              </button>
            ))}
          </div>
        </SettingsRow>
      </SettingsSection>
    </>
  );
}

function PageMCP({
  specs,
  initialEditRaw,
  initialEditNonce,
  onImportCcSwitch,
  onAdd,
  onRemove,
  onUpdate,
  onRetry,
}: {
  specs: McpSpecInfo[];
  bridged: boolean;
  initialEditRaw?: string | null;
  initialEditNonce?: number;
  onImportCcSwitch: () => Promise<void>;
  onAdd: (spec: string) => void;
  onRemove: (spec: string) => void;
  onUpdate: (raw: string, server: ImportedMcpServer) => void;
  onRetry: (raw: string) => void;
}) {
  const [draft, setDraft] = useState("");
  const [importing, setImporting] = useState(false);
  const [editing, setEditing] = useState<McpSpecInfo | null>(null);
  const appliedEditNonceRef = useRef<number | null>(null);
  useEffect(() => {
    if (!initialEditRaw) return;
    const nonce = initialEditNonce ?? 0;
    if (appliedEditNonceRef.current === nonce) return;
    const target = specs.find((s) => s.raw === initialEditRaw);
    if (target && !target.parseError) {
      appliedEditNonceRef.current = nonce;
      setEditing(target);
    }
  }, [initialEditRaw, initialEditNonce, specs]);
  const connectedCount = specs.filter((s) => s.status === "connected").length;
  const failedCount = specs.filter((s) => s.status === "failed").length;
  const disabledCount = specs.filter((s) => s.status === "disabled").length;
  const connectingCount = specs.filter(
    (s) => s.status === "configured" || s.status === "handshake",
  ).length;
  const statusKind =
    specs.length === 0
      ? "empty"
      : failedCount > 0
        ? "failed"
        : connectedCount === specs.length
          ? "connected"
          : connectingCount > 0
            ? "connecting"
            : disabledCount > 0
              ? "disabled"
              : "pending";
  const statusText =
    statusKind === "connected"
      ? t("settings.mcpStatusConnected", { connected: connectedCount, total: specs.length })
      : statusKind === "failed"
        ? t("settings.mcpStatusFailed", {
            connected: connectedCount,
            total: specs.length,
            failed: failedCount,
          })
        : statusKind === "connecting"
          ? t("settings.mcpStatusConnecting", { connected: connectedCount, total: specs.length })
          : statusKind === "disabled"
            ? t("settings.mcpStatusDisabled", { disabled: disabledCount, total: specs.length })
            : t("settings.mcpStatusPending");
  const submit = () => {
    const v = draft.trim();
    if (!v) return;
    onAdd(v);
    setDraft("");
  };
  const mcpSpecHint = t("settings.mcpSpecFormat")
    .replaceAll("<code>", "")
    .replaceAll("</code>", "");
  if (editing) {
    return (
      <McpEditPage
        spec={editing}
        onBack={() => setEditing(null)}
        onSave={(raw, server) => {
          onUpdate(raw, server);
          setEditing(null);
        }}
      />
    );
  }
  return (
    <>
      <section className="section">
        <div className="mcp-section-head">
          <div className="stitle">
            {t("settings.mcpConfigured", { count: specs.length })}
            <span className="mcp-status-summary" data-status={statusKind}>
              {statusText}
            </span>
          </div>
          <button
            type="button"
            className="btn"
            disabled={importing}
            onClick={async () => {
              setImporting(true);
              try {
                await onImportCcSwitch();
              } finally {
                setImporting(false);
              }
            }}
          >
            {importing ? t("settings.mcpImporting") : t("settings.mcpImport")}
          </button>
        </div>
        {specs.length === 0 ? (
          <SettingsEmptyState>{t("settings.mcpEmpty")}</SettingsEmptyState>
        ) : (
          specs.map((s) => (
            <McpServerCard
              key={s.raw}
              spec={s}
              mode="settings"
              onEdit={setEditing}
              onRetry={onRetry}
              onRemove={onRemove}
            />
          ))
        )}
      </section>
      <SettingsSection title={t("settings.mcpAddSection")}>
        <SettingsRow label={t("settings.mcpSpecLabel")} hint={mcpSpecHint}>
          <input
            className="field mono"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder="github=npx -y @smithery/cli ..."
            onKeyDown={(e) => {
              if (e.key === "Enter") submit();
            }}
          />
          <button type="button" className="btn primary" disabled={!draft.trim()} onClick={submit}>
            {t("settings.mcpAdd")}
          </button>
        </SettingsRow>
      </SettingsSection>
    </>
  );
}

const MCP_NAME_PREFIX = /^([a-zA-Z_][a-zA-Z0-9_-]*)=(.*)$/;
const MCP_STREAMABLE_PREFIX = /^streamable\+(https?:\/\/.+)$/i;
const MCP_HTTP_URL = /^https?:\/\//i;

function splitMcpArgs(body: string): string[] {
  const out: string[] = [];
  let current = "";
  let quote: '"' | "'" | null = null;
  let escaping = false;
  for (const ch of body) {
    if (escaping) {
      current += ch;
      escaping = false;
      continue;
    }
    if (ch === "\\") {
      escaping = true;
      continue;
    }
    if (quote) {
      if (ch === quote) quote = null;
      else current += ch;
      continue;
    }
    if (ch === '"' || ch === "'") {
      quote = ch;
      continue;
    }
    if (/\s/.test(ch)) {
      if (current) {
        out.push(current);
        current = "";
      }
      continue;
    }
    current += ch;
  }
  if (current) out.push(current);
  return out;
}

function mcpServerFromRawSpec(spec: McpSpecInfo): ImportedMcpServer | undefined {
  const trimmed = spec.raw.trim();
  if (!trimmed) return undefined;
  const match = MCP_NAME_PREFIX.exec(trimmed);
  const name = match?.[1] ?? spec.name ?? "";
  const body = (match?.[2] ?? trimmed).trim();
  if (!name || !body) return undefined;
  const streamable = MCP_STREAMABLE_PREFIX.exec(body);
  if (streamable) {
    return { name, transport: "streamable-http", url: streamable[1] };
  }
  if (MCP_HTTP_URL.test(body)) {
    return {
      name,
      transport: spec.transport === "streamable-http" ? "streamable-http" : "sse",
      url: body,
    };
  }
  const argv = splitMcpArgs(body);
  const [command, ...args] = argv;
  if (!command) return undefined;
  return { name, transport: "stdio", command, args };
}

function editableMcpServer(spec: McpSpecInfo): ImportedMcpServer | undefined {
  return spec.config ?? mcpServerFromRawSpec(spec);
}

function mcpServerToJson(server: ImportedMcpServer | undefined): Record<string, unknown> {
  if (!server) return {};
  const out: Record<string, unknown> = {};
  if (server.transport === "stdio") {
    out.command = server.command ?? "";
    out.args = server.args ?? [];
    if (server.env && Object.keys(server.env).length > 0) out.env = server.env;
    if (server.cwd) out.cwd = server.cwd;
  } else {
    out.url = server.url ?? "";
    if (server.headers && Object.keys(server.headers).length > 0) out.headers = server.headers;
  }
  if (server.disabled === true) out.disabled = true;
  if (typeof server.requestTimeoutMs === "number") out.requestTimeoutMs = server.requestTimeoutMs;
  return out;
}

function normalizeStringRecordForMcp(value: unknown): Record<string, string> | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  const out: Record<string, string> = {};
  for (const [key, entry] of Object.entries(value)) {
    if (typeof entry === "string") out[key] = entry;
  }
  return Object.keys(out).length > 0 ? out : undefined;
}

function normalizeMcpJsonDraft(
  name: string,
  transport: ImportedMcpServer["transport"],
  value: unknown,
): ImportedMcpServer {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(t("settings.mcpEditJsonObjectRequired"));
  }
  const raw = value as Record<string, unknown>;
  const requestTimeoutMs =
    typeof raw.requestTimeoutMs === "number" && Number.isFinite(raw.requestTimeoutMs)
      ? raw.requestTimeoutMs
      : undefined;
  const disabled = raw.disabled === true ? true : undefined;
  if (transport === "stdio") {
    const command = typeof raw.command === "string" ? raw.command.trim() : "";
    if (!command) throw new Error(t("settings.mcpEditCommandRequired"));
    return {
      name,
      transport,
      command,
      args: Array.isArray(raw.args)
        ? raw.args.filter((a): a is string => typeof a === "string")
        : [],
      env: normalizeStringRecordForMcp(raw.env),
      cwd: typeof raw.cwd === "string" && raw.cwd.trim() ? raw.cwd.trim() : undefined,
      disabled,
      requestTimeoutMs,
    };
  }
  const url = typeof raw.url === "string" ? raw.url.trim() : "";
  if (!url) throw new Error(t("settings.mcpEditUrlRequired"));
  return {
    name,
    transport,
    url,
    headers: normalizeStringRecordForMcp(raw.headers),
    disabled,
    requestTimeoutMs,
  };
}

function McpEditPage({
  spec,
  onBack,
  onSave,
}: {
  spec: McpSpecInfo;
  onBack: () => void;
  onSave: (raw: string, server: ImportedMcpServer) => void;
}) {
  const initial = editableMcpServer(spec);
  const [name, setName] = useState(initial?.name ?? spec.name ?? "");
  const [transport, setTransport] = useState<ImportedMcpServer["transport"]>(
    initial?.transport ?? spec.transport,
  );
  const [jsonDraft, setJsonDraft] = useState(() =>
    JSON.stringify(mcpServerToJson(initial), null, 2),
  );
  const [error, setError] = useState<string | null>(null);

  const formatJson = () => {
    try {
      const parsed = JSON.parse(jsonDraft);
      setJsonDraft(JSON.stringify(parsed, null, 2));
      setError(null);
    } catch (err) {
      setError((err as Error).message);
    }
  };

  const save = () => {
    const nextName = name.trim();
    if (!nextName) {
      setError(t("settings.mcpEditNameRequired"));
      return;
    }
    try {
      const parsed = JSON.parse(jsonDraft);
      onSave(spec.raw, normalizeMcpJsonDraft(nextName, transport, parsed));
    } catch (err) {
      setError((err as Error).message);
    }
  };

  return (
    <div className="mcp-edit">
      <div className="mcp-edit-top">
        <button type="button" className="btn ghost mcp-back-btn" onClick={onBack}>
          <I.chevR size={14} className="mcp-back-icon" />
          {t("settings.mcpEditBack")}
        </button>
        <div className="stitle">{t("settings.mcpEditTitle")}</div>
      </div>
      <section className="section mcp-edit-section">
        <label className="mcp-edit-field">
          <span>{t("settings.mcpEditName")}</span>
          <input className="field mono" value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <label className="mcp-edit-field">
          <span>{t("settings.mcpEditTransport")}</span>
          <select
            className="field mono"
            value={transport}
            onChange={(e) => setTransport(e.target.value as ImportedMcpServer["transport"])}
          >
            <option value="stdio">stdio</option>
            <option value="sse">sse</option>
            <option value="streamable-http">streamable-http</option>
          </select>
        </label>
      </section>
      <section className="section mcp-edit-section">
        <div className="mcp-edit-json-head">
          <div className="stitle">{t("settings.mcpEditJson")}</div>
          <button type="button" className="btn ghost" onClick={formatJson}>
            {t("settings.mcpEditFormat")}
          </button>
        </div>
        <textarea
          className="mcp-json-editor"
          value={jsonDraft}
          spellCheck={false}
          onChange={(e) => setJsonDraft(e.target.value)}
        />
        {error ? <div className="mcp-edit-error">{error}</div> : null}
      </section>
      <div className="mcp-edit-footer">
        <button type="button" className="btn ghost" onClick={onBack}>
          {t("revision.cancel")}
        </button>
        <button type="button" className="btn primary" onClick={save}>
          <I.file size={14} />
          {t("settings.mcpEditSave")}
        </button>
      </div>
    </div>
  );
}

function PageSkills({
  skills,
  subagentModels,
  onSave,
}: {
  skills: SkillInfo[];
  subagentModels: Record<string, "flash" | "pro">;
  onSave: (patch: SettingsPatch) => void;
}) {
  const setSubagentModel = (name: string, value: "flash" | "pro") => {
    onSave({ subagentModels: { ...subagentModels, [name]: value } });
  };
  return (
    <SettingsSection title={t("settings.skillsLoaded", { count: skills.length })}>
      {skills.length === 0 ? (
        <SettingsEmptyState>{t("settings.skillsEmpty")}</SettingsEmptyState>
      ) : (
        skills.map((s) => (
          <div className="scard" key={`${s.scope}:${s.name}`}>
            <div className="top">
              <span className="ico">
                <I.zap size={14} />
              </span>
              <div>
                <div className="nm">
                  <span className="settings-mono-accent">/{s.name}</span>
                </div>
                <div className="sub">
                  {s.scope} · {s.runAs}
                  {s.model ? ` · ${s.model}` : ""}
                </div>
              </div>
              {s.runAs === "subagent" ? (
                <select
                  className="field skill-model-field"
                  value={subagentModels[s.name] ?? "flash"}
                  onChange={(e) => setSubagentModel(s.name, e.target.value as "flash" | "pro")}
                  title={t("settings.subagentModelHint")}
                >
                  <option value="flash">{t("settings.subagentModelFlash")}</option>
                  <option value="pro">{t("settings.subagentModelPro")}</option>
                </select>
              ) : null}
            </div>
            <div className="desc">{s.description}</div>
            <div className="settings-path-text">{s.path}</div>
          </div>
        ))
      )}
    </SettingsSection>
  );
}

function PageMemory({
  entries,
  detail,
  onRead,
}: {
  entries: MemoryEntryInfo[];
  detail: MemoryDetail | null;
  onRead: (path: string) => void;
}) {
  return (
    <SettingsSection title={t("settings.memorySection")}>
      {entries.length === 0 ? (
        <SettingsEmptyState>{t("settings.memoryDesc")}</SettingsEmptyState>
      ) : (
        <div className="memory-browser">
          <div className="memory-list">
            {entries.map((m) => (
              <button
                type="button"
                className="memory-item"
                data-active={detail?.path === m.path}
                key={m.path}
                onClick={() => onRead(m.path)}
              >
                <span className="memory-kind">{m.kind.replace("_", " ")}</span>
                <span className="memory-name">{m.description || m.name}</span>
                <span className="memory-path">{m.path}</span>
              </button>
            ))}
          </div>
          <pre className="memory-detail">{detail ? detail.body : t("settings.memoryDesc")}</pre>
        </div>
      )}
    </SettingsSection>
  );
}

function PageRules({
  settings,
  onSave,
}: {
  settings: SettingsType;
  onSave: (patch: SettingsPatch) => void;
}) {
  return (
    <>
      <SettingsSection title={t("settings.editMode")}>
        <SettingsRow label={t("settings.appMode")} hint={t("settings.editModeHint")}>
          <div className="seg-ctrl">
            {(["plan", "review", "auto", "yolo"] as const).map((m) => (
              <button
                type="button"
                key={m}
                data-on={settings.editMode === m}
                onClick={() => onSave({ editMode: m })}
              >
                {m}
              </button>
            ))}
          </div>
        </SettingsRow>
      </SettingsSection>
      <SettingsSection title={t("settings.ruleAutoApprovalSection")}>
        <SettingsEmptyState>{t("settings.ruleAutoApprovalHint")}</SettingsEmptyState>
      </SettingsSection>
    </>
  );
}

function PageBilling({
  balance,
  usage,
  currency,
}: {
  balance: Balance | null;
  usage: UsageStats;
  currency: "CNY" | "USD";
}) {
  const symbol = currency === "CNY" ? "¥" : "$";
  const sessionCost = currency === "CNY" ? usage.totalCostUsd * 7.2 : usage.totalCostUsd;
  const totalTokens = usage.cacheHitTokens + usage.cacheMissTokens;
  const hitPct = totalTokens > 0 ? Math.round((usage.cacheHitTokens / totalTokens) * 100) : 0;
  return (
    <>
      <div className="bill-grid">
        <div className="bill-card">
          <div className="l">{t("settings.balanceLabel")}</div>
          <div className="v ok">
            {balance
              ? `${balance.currency === "USD" ? "$" : "¥"} ${balance.total.toFixed(2)}`
              : "—"}
          </div>
          <div className="sub">
            {balance && !balance.isAvailable
              ? t("settings.balanceLow")
              : t("settings.balanceAvailable")}
          </div>
        </div>
        <div className="bill-card">
          <div className="l">{t("settings.sessionCost")}</div>
          <div className="v">
            {symbol} {sessionCost.toFixed(4)}
          </div>
          <div className="sub">prompt {usage.totalPromptTokens.toLocaleString()} t</div>
        </div>
        <div className="bill-card">
          <div className="l">{t("settings.cacheHitRate")}</div>
          <div className="v acc">{hitPct}%</div>
          <div className="sub">
            hit {usage.cacheHitTokens.toLocaleString()} / miss{" "}
            {usage.cacheMissTokens.toLocaleString()}
          </div>
        </div>
      </div>
    </>
  );
}

function PageShortcuts() {
  const rows: { nm: string; keys: ShortcutKey[] }[] = [
    { nm: t("settings.shortcutNewChat"), keys: ["mod", "N"] },
    { nm: t("settings.shortcutNewTab"), keys: ["mod", "T"] },
    { nm: t("settings.shortcutCloseTab"), keys: ["mod", "W"] },
    { nm: t("settings.shortcutCommandPalette"), keys: ["mod", "K"] },
    { nm: t("settings.shortcutFocusComposer"), keys: ["mod", "L"] },
    { nm: t("settings.shortcutSwitchTab"), keys: ["mod", "tab"] },
    { nm: t("settings.shortcutAbort"), keys: ["esc"] },
    { nm: t("settings.shortcutSettings"), keys: ["mod", ","] },
  ];
  return (
    <section className="section">
      <div className="kbd-grid">
        {rows.map((s) => (
          <SectionRow key={s.nm} nm={s.nm} keys={s.keys} />
        ))}
      </div>
    </section>
  );
}

function SectionRow({ nm, keys }: { nm: string; keys: ShortcutKey[] }): ReactNode {
  return (
    <>
      <div className="nm">{nm}</div>
      <div className="keys">
        <Shortcut keys={keys} />
      </div>
    </>
  );
}
