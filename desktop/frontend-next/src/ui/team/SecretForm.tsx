import { useState } from "react";
import { t } from "../../i18n";
import { reason } from "../../i18n/kernel";
import type { TeamPort, TeamSecret } from "../../port/team";

// A connection anyone between here and the kernel could read.
function plainOverNetwork(): boolean {
  if (typeof location === "undefined" || location.protocol !== "http:") return false;
  return !["localhost", "127.0.0.1", "[::1]"].includes(location.hostname);
}

interface Scope {
  key: string;
  label: string;
  agents?: string[];
}

// Who a save gives the secret to. The asker alone comes first; a secret
// already saved under the name keeps the agents it had, so answering one
// agent never takes the secret from another unseen.
function scopes(agent: string, current: TeamSecret | undefined, nameOf: (id: string) => string): { options: Scope[]; initial: string } {
  const only = (ids: string[]) => t("只给 {name}", { name: ids.map(nameOf).join("、") });
  const options: Scope[] = [{ key: "agent", label: only([agent]), agents: [agent] }];
  const kept = current?.agents ? [...new Set([...current.agents, agent])] : undefined;
  if (kept && kept.length > 1) options.push({ key: "kept", label: only(kept), agents: kept });
  options.push({ key: "all", label: t("所有 Agent") });
  const initial = !current ? "agent" : !current.agents ? "all" : kept && kept.length > 1 ? "kept" : "agent";
  return { options, initial };
}

// Filling in a secret an agent asked for. The value lives in this form only
// until it is sent; nothing reads it back.
export function SecretForm({ team, name, purpose, agent, current, nameOf, onDone }: {
  team: TeamPort;
  name: string;
  purpose?: string;
  agent: string;
  current?: TeamSecret;
  nameOf: (id: string) => string;
  onDone: (outcome: "saved" | "declined") => void;
}) {
  const { options, initial } = scopes(agent, current, nameOf);
  const [value, setValue] = useState("");
  const [scope, setScope] = useState(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const chosen = options.find((o) => o.key === scope) ?? options[0];
  const run = async (write: () => Promise<void>, outcome: "saved" | "declined") => {
    setBusy(true);
    setError("");
    try {
      await write();
      setValue("");
      onDone(outcome);
    } catch (e) {
      setError(reason(e));
      setBusy(false);
    }
  };
  return (
    <form className="team-secret-form" data-action-submit="team.secret-save" data-target={name}
      onSubmit={(ev) => { ev.preventDefault(); if (value) void run(() => team.saveSecret({ name, value, purpose, agents: chosen.agents, requester: agent }), "saved"); }}>
      {purpose && <p className="team-secret-purpose">{purpose}</p>}
      {current && <p className="team-note">{t("已经保存过 {name}，保存会替换它的值", { name })}</p>}
      <label>{t("密钥")}
        <input type="password" autoComplete="off" spellCheck={false} data-action-change="team.secret-value" value={value} disabled={busy}
          placeholder={t("粘贴或输入")} onChange={(ev) => setValue(ev.target.value)} />
      </label>
      <div className="team-secret-scope" role="radiogroup" aria-label={t("给谁用")}>
        {options.map((o) => (
          <label key={o.key}><input type="radio" name={`scope-${name}-${agent}`} data-action-change="team.secret-scope" checked={chosen.key === o.key} onChange={() => setScope(o.key)} />{o.label}</label>
        ))}
      </div>
      <p className="team-note">{t("命令以 ${env} 读到它；Reasonix 发给模型的内容里，它显示为 [secret:{name}]。命令能读出并转写它，所以只交给你信任的 Agent。", { name, env: `REASONIX_SECRET_${name}` })}</p>
      {plainOverNetwork() && <p className="team-error">{t("这个页面没有加密，密钥会以明文经过网络")}</p>}
      {error && <p className="team-error" role="alert">{error}</p>}
      <div className="team-secret-actions">
        <button type="button" data-action="team.secret-decline" data-target={name} disabled={busy} onClick={() => void run(() => team.cancelSecret(name, agent), "declined")}>{t("不提供")}</button>
        <button type="submit" className="team-save" disabled={busy || !value}>{t("保存")}</button>
      </div>
    </form>
  );
}
