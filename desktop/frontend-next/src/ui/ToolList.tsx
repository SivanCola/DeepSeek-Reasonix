import { useEffect, useState } from "react";
import { t } from "../i18n";
import { reason } from "../i18n/kernel";
import type { AgentPort, ToolSettings } from "../port/port";
import { Group } from "./Group";
import { Switch } from "./Switch";
import { toolBlurb } from "./toolBlurbs";

// Every tool the model is offered, each with a switch. Turning one off removes
// it from every request in conversations opened afterwards and refuses a call to
// it; a tool another feature needs has no switch and says which feature.
// What needs a tool, by the kernel's feature id; an id this build does not know is shown as sent.
function feature(id: string): string {
  if (id === "goal_mode") return t("目标模式");
  if (id === "deferred_mcp_tools") return t("加载延后的 MCP 工具");
  if (id === "plan_steps") return t("计划步骤跟踪");
  if (id === "turn_exit") return t("结束一个宿主留着没完的回合");
  return id;
}

export function ToolList({ port }: { port: AgentPort }) {
  const [state, setState] = useState<ToolSettings | null>(null);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    port
      .toolSettings()
      .then(setState)
      .catch(() => setState(null));
  }, [port]);

  const group = (body: React.ReactNode) => (
    <Group id="tool-list" title={t("模型可用的工具")} hint={t("关掉的工具不会再出现在发给模型的请求里，模型也调用不了它；可以省下每次请求里的提示词开销。新开的对话生效，已有对话不受影响。")}>
      {body}
    </Group>
  );

  if (!state) return group(<div className="empty">{t("无法读取工具列表。")}</div>);

  const flip = async (name: string, disabled: boolean) => {
    setBusy(name);
    setError("");
    try {
      setState(await port.saveToolDisabled(name, disabled));
    } catch (e) {
      setError(reason(e));
    } finally {
      setBusy("");
    }
  };

  return group(
    <div className="box">
      {state.tools.map((tool) => (
        <div className="lrow" key={tool.name}>
          <span className="tx">
            <span className="lb">{tool.name}</span>
            <span className="ds">
              {tool.requiredBy
                ? t("{feature}需要它，不能关闭。", { feature: feature(tool.requiredBy) })
                : tool.byProject
                  ? t("这个项目的配置文件把它关掉了；要重新打开，请在项目的 reasonix.toml 里去掉。")
                  : (toolBlurb(tool.name) ?? tool.description)}
            </span>
          </span>
          {!tool.requiredBy && !tool.byProject && (
            <Switch
              data-action="tool-list.toggle"
              on={!tool.disabled}
              busy={busy === tool.name}
              label={tool.name}
              onClick={() => void flip(tool.name, !tool.disabled)}
            />
          )}
        </div>
      ))}
      {error && <div className="why">{error}</div>}
    </div>,
  );
}
