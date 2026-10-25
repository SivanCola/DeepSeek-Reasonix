import { useState } from "react";
import { t } from "../i18n";
import { reason } from "../i18n/kernel";
import type { AgentPort } from "../port/port";
import { saveChangedFilesPref, useShowsChangedFiles } from "../state/changedfiles";
import { Group } from "./Group";
import { Switch } from "./Switch";

export function ChangedFilesSetting({ port }: { port: AgentPort }) {
  const on = useShowsChangedFiles(port);
  const [error, setError] = useState("");
  return (
    <Group id="changed-files" title={t("文件改动")} hint={t("在输入框上方显示 Git 工作区的文件改动。关闭后，数量显示在分支标签旁；统计与本轮是否改过文件无关。")}
      action={<Switch data-action="chrome.changed-files" on={on} label={t("在输入框上方显示文件改动")} onClick={() => {
          setError("");
          saveChangedFilesPref(port, !on).catch((e) => setError(reason(e)));
        }} />}>
      {error && <div className="why">{error}</div>}
    </Group>
  );
}
