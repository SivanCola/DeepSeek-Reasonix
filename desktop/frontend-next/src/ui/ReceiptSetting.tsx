import { useState } from "react";
import { t } from "../i18n";
import { reason } from "../i18n/kernel";
import type { AgentPort } from "../port/port";
import { useShowsReceipt } from "../state/foldpref";
import { receiptPrefOf, saveReceiptPref } from "../state/receiptpref";
import { Group } from "./Group";
import { Switch } from "./Switch";

export function ReceiptSetting({ port }: { port: AgentPort }) {
  const on = useShowsReceipt(receiptPrefOf(port));
  const [error, setError] = useState("");
  return (
    <Group id="receipt" title={t("显示交付验收卡片")} hint={t("仅控制显示，记录始终保留。默认关闭；桌面已有会话立即更新，终端下次启动生效。")}
      action={<Switch data-action="chrome.receipt" on={on} label={t("显示交付验收卡片")} onClick={() => {
          setError("");
          saveReceiptPref(port, !on).catch((e) => setError(reason(e)));
        }} />}>
      {error && <div className="why">{error}</div>}
    </Group>
  );
}
