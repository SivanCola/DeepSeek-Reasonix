import { useEffect, useState, type ReactNode } from "react";
import { t } from "../i18n";
import { reason } from "../i18n/kernel";
import type { AgentPort, DefaultModeSettings } from "../port/port";
import { Group } from "./Group";
import { Switch } from "./Switch";

// One switch over how a new conversation starts: in plan mode, or as usual. Off
// is the default. It changes only the starting mode; the mode picker still
// switches it, and a conversation that already exists keeps its own.
export function DefaultMode({ port }: { port: AgentPort }) {
  const [state, setState] = useState<DefaultModeSettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    port
      .defaultMode()
      .then(setState)
      .catch(() => setState(null));
  }, [port]);

  const group = (body: ReactNode) => (
    <Group id="default-mode" title={t("新对话默认进入计划模式")} hint={t("默认关闭。开启后，新会话先规划、经你批准后执行；已有会话不变，也可随时切换模式。仅保存在用户配置，项目不能更改。")}
      action={state && <Switch data-action="default-mode.plan" on={state.plan} busy={busy}
        label={t("新对话默认进入计划模式")} onClick={() => void flip()} />}>
      {body}
    </Group>
  );

  if (!state) return group(<div className="empty">{t("无法读取新对话的起始模式设置。")}</div>);

  const flip = async () => {
    setBusy(true);
    setError("");
    try {
      setState(await port.saveDefaultMode(!state.plan));
    } catch (e) {
      setError(reason(e));
    } finally {
      setBusy(false);
    }
  };

  return group(error ? <div className="why">{error}</div> : null);
}
