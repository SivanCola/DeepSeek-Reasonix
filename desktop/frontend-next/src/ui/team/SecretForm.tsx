import { useState } from "react";
import { t } from "../../i18n";
import { reason } from "../../i18n/kernel";
import type { TeamPort } from "../../port/team";

// A connection anyone between here and the kernel could read.
function plainOverNetwork(): boolean {
  if (typeof location === "undefined" || location.protocol !== "http:") return false;
  return !["localhost", "127.0.0.1", "[::1]"].includes(location.hostname);
}

// Filling in a secret an agent asked for. The value lives in this form only
// until it is sent; nothing reads it back.
export function SecretForm({ team, name, purpose, agent, agentName, onDone }: {
  team: TeamPort;
  name: string;
  purpose?: string;
  agent: string;
  agentName: string;
  onDone: (outcome: "saved" | "declined") => void;
}) {
  const [value, setValue] = useState("");
  const [only, setOnly] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
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
      onSubmit={(ev) => { ev.preventDefault(); if (value) void run(() => team.saveSecret({ name, value, purpose, agents: only ? [agent] : undefined, requester: agent }), "saved"); }}>
      {purpose && <p className="team-secret-purpose">{purpose}</p>}
      <label>{t("密钥")}
        <input type="password" autoComplete="off" spellCheck={false} data-action-change="team.secret-value" value={value} disabled={busy}
          placeholder={t("粘贴或输入")} onChange={(ev) => setValue(ev.target.value)} />
      </label>
      <div className="team-secret-scope" role="radiogroup" aria-label={t("给谁用")}>
        <label><input type="radio" name={`scope-${name}`} data-action-change="team.secret-scope" checked={!only} onChange={() => setOnly(false)} />{t("所有 Agent")}</label>
        <label><input type="radio" name={`scope-${name}`} data-action-change="team.secret-scope" checked={only} onChange={() => setOnly(true)} />{t("只给 {name}", { name: agentName })}</label>
      </div>
      <p className="team-note">{t("它只以 ${env} 进入 {name} 的命令；发给模型的内容里会被遮住，但命令能读到它，只交给你信任的 Agent。", { name: agentName, env: `REASONIX_SECRET_${name}` })}</p>
      {plainOverNetwork() && <p className="team-error">{t("这个页面没有加密，密钥会以明文经过网络")}</p>}
      {error && <p className="team-error" role="alert">{error}</p>}
      <div className="team-secret-actions">
        <button type="button" data-action="team.secret-decline" data-target={name} disabled={busy} onClick={() => void run(() => team.cancelSecret(name, agent), "declined")}>{t("不提供")}</button>
        <button type="submit" className="team-save" disabled={busy || !value}>{t("保存")}</button>
      </div>
    </form>
  );
}
