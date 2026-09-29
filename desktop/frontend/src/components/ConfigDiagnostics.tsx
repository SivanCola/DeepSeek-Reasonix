import { useEffect, useState } from "react";
import { useConfigDiagnostics } from "../lib/useConfigDiagnostics";
import { useT } from "../lib/i18n";
import { CopyButton } from "./CopyButton";
import { normalizeConfigDiagnostics, type ConfigDiagnostic } from "../lib/configDiagnostics";
import { app } from "../lib/bridge";

function DiagnosticDetails({ item, tabId, hostId, workspaceId }: { item: ConfigDiagnostic; tabId: string; hostId: string; workspaceId: string }) {
  const t = useT();
  const [detail, setDetail] = useState<ConfigDiagnostic | null>(null);
  useEffect(() => {
    let current = true;
    void app.ConfigDiagnosticDetails?.(tabId, item.id).then(raw => {
      const result = normalizeConfigDiagnostics(raw);
      if (current && result?.hostId === hostId && result.workspaceId === workspaceId) {
        setDetail(result.items.find(value => value.id === item.id) ?? null);
      }
    }).catch(() => {});
    return () => { current = false; };
  }, [tabId, item.id, hostId, workspaceId]);
  const values = Array.isArray(detail?.values) ? detail.values.filter(value => typeof value === "string") : [];
  return <>
    <p>{item.source}</p><p>{item.field} · {item.code}</p>
    {["user_config_invalid", "project_config_invalid"].includes(item.code) && <p>{t("config.doctorHint")}</p>}
    {item.status === "awaiting_approval" && <p>{t("config.compatTrust")} <code>reasonix trust</code><CopyButton text="reasonix trust" /></p>}
    {values.length > 0 && <div className="config-compatibility__values">{values.map((value, index) => <pre key={index}>{value}</pre>)}</div>}
    <CopyButton text={values.length ? values.join("\n") : JSON.stringify(item, null, 2)} />
  </>;
}

function DiagnosticSummary({ item }: { item: ConfigDiagnostic }) {
  const t = useT();
  if (item.status === "on_demand") return <>{t(item.field === "permissions.allow" ? "config.compatCommands" : "config.compatPaths", { count: item.count })}</>;
  if (item.status === "awaiting_approval") return <>{t("config.compatProgram")}</>;
  if (item.code === "project_grants_unavailable") return <>{t("config.compatGrantError")}</>;
  if (["user_config_invalid", "project_config_invalid", "configuration_unavailable", "mcp_config_invalid"].includes(item.code)) return <>{t("config.compatReadError")}</>;
  return <>{t("config.compatProblem", { field: item.field || item.code })}</>;
}

export function ConfigDiagnosticBanner({ tabId, bindingKey }: { tabId: string; bindingKey: string }) {
  const t = useT();
  const state = useConfigDiagnostics(tabId, bindingKey);
  const [error, setError] = useState("");
  const [expanded, setExpanded] = useState<string | null>(null);
  if (state.warnings.length === 0) return null;
  return <div className="banner banner--warning banner--actionable config-compatibility">
    <div className="banner__msg">
      {state.warnings.map(item => <details key={item.id} onToggle={event => setExpanded(event.currentTarget.open ? item.id : null)}>
        <summary><DiagnosticSummary item={item} /></summary>
        {expanded === item.id && <DiagnosticDetails key={bindingKey + item.id} item={item} tabId={tabId} hostId={state.snapshot!.hostId} workspaceId={state.snapshot!.workspaceId} />}
        {state.snapshot?.hostId === "local" && <button className="btn btn--small" onClick={() => {
          void state.openSource(item.id).catch(() => setError(t("config.compatOpenFailed")));
        }}>{t("config.openConfig")}</button>}
        <button className="btn btn--small" onClick={() => state.dismiss(item.id)}>{t("updater.dismiss")}</button>
      </details>)}
      {error && <p role="alert">{error}</p>}
    </div>
    <span className="banner__spacer" />
    <button className="btn btn--small" onClick={() => { setError(""); void state.reload(); }}>{t("config.reloadConfig")}</button>
  </div>;
}

export function ConfigCompatibilitySettings({ workspaceKey }: { workspaceKey: string }) {
  const t = useT();
  const tabId = workspaceKey.split("\u0000")[0] ?? "";
  const state = useConfigDiagnostics(tabId, workspaceKey);
  const [error, setError] = useState("");
  const [expanded, setExpanded] = useState<string | null>(null);
  if (!tabId) return null;
  const status = state.loading ? t("config.compatLoading") : state.snapshot?.status === "unsupported" ? t("config.compatUnsupported") :
    state.unavailable || state.snapshot?.status === "unavailable" ? t("config.compatUnavailable") : state.items.length === 0 ? t("config.compatClear") : "";
  return <section className="settings-section config-compatibility" aria-label={t("config.compatTitle")}>
    <h3 className="settings-section__title">{t("config.compatTitle")}</h3>
    <p>{state.snapshot?.workspaceRoot}</p>
    {status && <p role="status">{status}</p>}
    {state.items.map(item => <details key={item.id} onToggle={event => setExpanded(event.currentTarget.open ? item.id : null)}>
      <summary><DiagnosticSummary item={item} /></summary>
      {expanded === item.id && <DiagnosticDetails key={workspaceKey + item.id} item={item} tabId={tabId} hostId={state.snapshot!.hostId} workspaceId={state.snapshot!.workspaceId} />}
      {state.snapshot?.hostId === "local" && <button className="btn btn--small" onClick={() => {
        void state.openSource(item.id).catch(() => setError(t("config.compatOpenFailed")));
      }}>{t("config.openConfig")}</button>}
    </details>)}
    {error && <p role="alert">{error}</p>}
    <button className="btn btn--small" onClick={() => { setError(""); void state.reload(); }}>{t("config.reloadConfig")}</button>
    <p>{t("config.compatReloadHint")}</p>
  </section>;
}
