import { useState } from "react";
import { t } from "../i18n";
import { reason } from "../i18n/kernel";
import type { AgentPort } from "../port/port";
import { saveFoldWorkPref, useFoldsTurnWork } from "../state/foldwork";
import { Group } from "./Group";
import { Switch } from "./Switch";

export function FoldWorkSetting({ port }: { port: AgentPort }) {
  const on = useFoldsTurnWork(port);
  const [error, setError] = useState("");
  return (
    <Group id="fold-work" title={t("折叠中间过程")} hint={t("结束后将整轮过程收成一行，保留最后答复与错误标记；点击可展开。运行中保持展开，默认关闭。")}
      action={<Switch data-action="chrome.fold-work" on={on} label={t("折叠一轮里的中间过程")} onClick={() => {
          setError("");
          saveFoldWorkPref(port, !on).catch((e) => setError(reason(e)));
        }} />}>
      {error && <div className="why">{error}</div>}
    </Group>
  );
}
