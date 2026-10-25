import { STORAGE as LANG_KEY, t } from "../i18n";
import type { AgentPort, Appearance } from "../port/port";
import { Group, SettingSection } from "./Group";
import { WindowSection } from "./WindowSection";
import { Notifications } from "./Notifications";
import { SendShortcut } from "./SendShortcut";
import { EscStopSetting } from "./EscStopSetting";
import { NavRailSection } from "./NavRailSection";
import { SessionTabsSetting } from "./SessionTabsSetting";
import { SessionQuickSection } from "./SessionQuickSection";
import { LiveGraceSection } from "./LiveGraceSection";
import { LegacySessionsSection } from "./LegacySessionsSection";
import { OpenSessionLimitSection } from "./OpenSessionLimitSection";
import { LiveStreamBudgetSection } from "./LiveStreamBudgetSection";
import { GraphicsSection } from "./GraphicsSection";
import { NavEntriesSection } from "./NavEntriesSection";
import { NewAnswerSection } from "./NewAnswerSection";
import { KeysSheet } from "./KeysSheet";
import { PageSleepSetting } from "./PageSleepSetting";

const LANGUAGES = [["", "跟随系统"], ["zh", "中文"], ["en", "英文"]];

export function GeneralSettings({ port, look, onLook }: {
  port: AgentPort; look: Appearance; onLook: (look: Appearance) => void;
}) {
  const language = localStorage.getItem(LANG_KEY) ?? look.language ?? "";
  return <>
    <SettingSection ids={["language", "window", "notify"]}>
    <Group id="language" title={t("语言")} hint={t("仅影响界面语言，更改后重新载入窗口。")}
      action={<div className="seg" data-text role="group" aria-label={t("语言")}>
        {LANGUAGES.map(([id, name]) =>
          <button key={id} data-action="appearance.language" data-value={id} aria-pressed={language === id} onClick={() => {
            localStorage.setItem(LANG_KEY, id);
            onLook({ ...look, language: id });
            setTimeout(() => location.reload(), 120);
          }}>{t(name)}</button>)}
      </div>} />
    <WindowSection port={port} />
    <Notifications port={port} />
    </SettingSection>
    <SettingSection title={t("输入与快捷键")} ids={["send-shortcut", "keys", "esc-stop"]}>
    <SendShortcut />
    <KeysSheet />
    <EscStopSetting />
    </SettingSection>
    <details className="prefs-disclosure">
      <summary>{t("导航与会话列表")}</summary>
      <SettingSection ids={["navrail", "naventries", "session-tabs", "session-quick-actions", "new-answer-marker", "live-grace", "legacy-sessions"]}>
      <NavRailSection />
      <NavEntriesSection />
      <SessionTabsSetting />
      <SessionQuickSection />
      <NewAnswerSection />
      <LiveGraceSection />
      <LegacySessionsSection />
      </SettingSection>
    </details>
    <details className="prefs-disclosure">
      <summary>{t("性能与连接")}</summary>
      <SettingSection title={t("会话资源")} ids={["open-session-limit", "live-stream-budget"]}>
      <OpenSessionLimitSection />
      <LiveStreamBudgetSection />
      <details className="settings-help resource-help"><summary>{t("资源使用说明")}</summary>
        <p>{t("保留的标签越多，内存占用越高。实时连接数量独立控制后台更新，不会关闭标签或停止任务；连接过多可能影响发送和停止请求。")}</p>
      </details>
      </SettingSection>
      <PageSleepSetting />
      <GraphicsSection />
    </details>
  </>;
}
