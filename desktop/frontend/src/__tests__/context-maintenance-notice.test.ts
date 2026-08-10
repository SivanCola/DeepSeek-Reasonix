import { readFileSync } from "node:fs";
import { formatContextMaintenanceNotice } from "../lib/contextMaintenanceTypes";
import type { DictKey, Translator } from "../lib/i18n";

function ok(value: unknown, message: string) {
  if (!value) throw new Error(message);
}

const messages: Partial<Record<DictKey, string>> = {
  "context.maintenanceTitle": "上下文短视图",
  "context.maintenanceAppliedSummary": "已生成上下文短视图 · 历史已摘要",
  "context.maintenanceBlockedSummary": "上下文摘要未能形成安全短视图 · 已停止自动重试",
  "context.maintenanceFailedSummary": "上下文摘要失败 · 已停止自动重试",
  "context.tokensValue": "{value} tokens",
  "summary.detail": "摘要",
};

const translate: Translator = (key, vars) => {
  const value = messages[key] ?? key;
  return value.replace(/\{(\w+)\}/g, (_, name: string) => String(vars?.[name] ?? `{${name}}`));
};

const applied = formatContextMaintenanceNotice({
  status: "applied",
  action: "summary",
  inputTokens: 120,
  resultTokens: 80,
  savedTokens: 40,
}, translate);
ok(applied === "已生成上下文短视图 · 历史已摘要", `unexpected applied notice: ${applied}`);

const blocked = formatContextMaintenanceNotice({ status: "blocked" }, translate);
ok(blocked === "上下文摘要未能形成安全短视图 · 已停止自动重试", `unexpected blocked notice: ${blocked}`);

const failed = formatContextMaintenanceNotice({ status: "failed" }, translate);
ok(failed === "上下文摘要失败 · 已停止自动重试", `unexpected failed notice: ${failed}`);

const contextPanelSource = readFileSync(new URL("../components/ContextPanel.tsx", import.meta.url), "utf8");
ok(
  contextPanelSource.includes("context.maintenanceCanonical"),
  "ContextPanel must show canonical vs model-visible tokens",
);
ok(
  contextPanelSource.includes("triggerTokens") || contextPanelSource.includes("maintenance?.triggerTokens"),
  "ContextPanel must surface triggerTokens",
);
ok(
  contextPanelSource.includes("checkpointState"),
  "ContextPanel must surface checkpointState",
);
ok(
  !contextPanelSource.includes("snipTrigger") && !contextPanelSource.includes("forceTrigger"),
  "ContextPanel must not present retired multi-threshold triggers as user settings",
);

console.log("context-maintenance-notice: ok");
