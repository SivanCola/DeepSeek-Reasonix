import { useEffect, useRef, useState } from "react";
import { t } from "../i18n";
import { reason } from "../i18n/kernel";
import type { AgentPort } from "../port/port";

export function AgentGuidance({ port, runRef, running }: { port: AgentPort; runRef: string; running: boolean }) {
  const [draft, setDraft] = useState("");
  const [state, setState] = useState<{ kind: "idle" | "sending" | "queued" | "failed"; error?: string }>({ kind: "idle" });
  const pending = useRef(false);
  const lifetime = useRef(0);
  useEffect(() => {
    lifetime.current++;
    pending.current = false;
    setState({ kind: "idle" });
    return () => { lifetime.current++; };
  }, [port, runRef]);

  const send = async () => {
    const text = draft.trim();
    if (!text || !running || pending.current) return;
    pending.current = true;
    const generation = lifetime.current;
    setState({ kind: "sending" });
    try {
      await port.steerSubagent(runRef, text);
      if (lifetime.current !== generation) return;
      setDraft((current) => current === draft ? "" : current);
      setState({ kind: "queued" });
    } catch (error) {
      if (lifetime.current === generation) setState({ kind: "failed", error: reason(error) });
    } finally {
      if (lifetime.current === generation) pending.current = false;
    }
  };

  if (!running && !draft && state.kind === "idle") return null;
  return (
    <form className="agentrun-guidance" data-action="agent.steer" data-target={runRef} onSubmit={(event) => { event.preventDefault(); void send(); }}>
      <label>
        <span>{t("追加指导")}</span>
        <textarea data-action="agent.guidance-draft" data-target={runRef} aria-label={t("追加指导")} value={draft} rows={2} placeholder={t("补充要求或调整任务方向…")}
          disabled={!running} onChange={(event) => { setDraft(event.target.value); if (!pending.current) setState({ kind: "idle" }); }} />
      </label>
      <div className="agentrun-guidance-actions">
        <span className="agentrun-guidance-note">
          {!running ? t("子代理已结束，未发送的指导保留在此。") : t("发送给当前子代理")}
        </span>
        <button className="btn btn--small" type="submit" disabled={!running || !draft.trim() || state.kind === "sending"}>
          {t("发送指导")}
        </button>
      </div>
      {state.kind === "queued" && <p role="status">{t("指导已排队，将在下一个可处理的边界读取。")}</p>}
      {state.kind === "failed" && <p role="alert">{state.error}</p>}
    </form>
  );
}
