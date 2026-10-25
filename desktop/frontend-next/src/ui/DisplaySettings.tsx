import type { AgentPort } from "../port/port";
import { ChangedFilesSetting } from "./ChangedFilesSetting";
import { FeedbackLevelNoticeSetting } from "./FeedbackLevelNoticeSetting";
import { FoldWorkSetting } from "./FoldWorkSetting";
import { TurnNarrationSetting } from "./TurnNarrationSetting";
import { ReceiptSetting } from "./ReceiptSetting";
import { Folding } from "./Folding";
import { t } from "../i18n";
import { SettingSection } from "./Group";

// What the conversation screen shows, each kept by the kernel the same way.
export function DisplaySettings({ port }: { port: AgentPort }) {
  return (
    <SettingSection ids={["changed-files", "fold-work", "turn-narration", "receipt", "folding"]}>
      <ChangedFilesSetting port={port} />
      <FeedbackLevelNoticeSetting port={port} />
      <FoldWorkSetting port={port} />
      <p className="note">{t("上方控制整轮中间过程；下方分别设置思考、工具步骤和输出的展开方式。")}</p>
      <Folding />
      <TurnNarrationSetting port={port} />
      <ReceiptSetting port={port} />
    </SettingSection>
  );
}
