import { useCallback, useEffect, useMemo, useState } from "react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import type { CapabilitiesView, MCPServerInput, ServerView, SkillView } from "../lib/types";
import { ResizableDrawer } from "./ResizableDrawer";

// CapabilitiesPanel is the desktop MCP & Skills drawer — the GUI counterpart to
// the CLI's /mcp + /skill, aligning with Claude Code's Customize → Connectors:
// each server shows a connected/failed dot, transport, and tool/prompt/resource
// counts, with add / remove / retry; skills list their scope and run mode.
type CapTab = "servers" | "skills";

export function CapabilitiesPanel({
  onClose,
}: {
  onClose: () => void;
}) {
  const t = useT();
  const [view, setView] = useState<CapabilitiesView | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [confirming, setConfirming] = useState<string | null>(null);
  const [tab, setTab] = useState<CapTab>("servers");
  const [skillQuery, setSkillQuery] = useState("");
  const [expandedSkills, setExpandedSkills] = useState<Set<string>>(() => new Set());
  const [expandedErrors, setExpandedErrors] = useState<Set<string>>(() => new Set());

  const reload = async () =>
    setView(await app.Capabilities().catch(() => ({ servers: [], skills: [] })));
  useEffect(() => {
    void reload();
  }, []);

  // mutate runs an MCP edit, re-reads the snapshot, and surfaces any failure as an
  // inline banner (a connect error, a missing binary, a bad URL).
  const mutate = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setErr(null);
    try {
      await fn();
      await reload();
      return true;
    } catch (e) {
      setErr(String((e as Error)?.message ?? e));
      return false;
    } finally {
      setBusy(false);
    }
  };

  const summary = useMemo(() => {
    if (!view) return "";
    return t("caps.summary", {
      connected: view.servers.filter((s) => s.status === "connected").length,
      failed: view.servers.filter((s) => s.status === "failed").length,
      skills: view.skills.length,
    });
  }, [view, t]);

  const filteredSkills = useMemo(() => {
    if (!view) return [];
    const q = skillQuery.trim().toLowerCase();
    if (!q) return view.skills;
    return view.skills.filter((sk) => {
      const text = [sk.name, `/${sk.name}`, sk.description, sk.scope, sk.runAs].join(" ").toLowerCase();
      return text.includes(q);
    });
  }, [view, skillQuery]);

  const serverGroups = useMemo(() => {
    const servers = view?.servers ?? [];
    return {
      failed: servers.filter((s) => s.status === "failed"),
      connected: servers.filter((s) => s.status === "connected"),
      disabled: servers.filter((s) => s.status === "disabled"),
    };
  }, [view]);

  const toggleSkill = useCallback((name: string) => {
    setExpandedSkills((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  }, []);

  const toggleError = useCallback((name: string) => {
    setExpandedErrors((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  }, []);

  return (
    <ResizableDrawer onClose={onClose} subtle>
        <header className="drawer__head">
          <div>
            <div className="drawer__title">{t("caps.title")}</div>
            {view && <div className="drawer__summary">{summary}</div>}
          </div>
          <button className="chip" onClick={onClose} title={t("common.close")}>
            ✕
          </button>
        </header>

        {!view ? (
          <div className="empty">{t("caps.loading")}</div>
        ) : (
          <div className="drawer__body">
            {err && <div className="banner banner--error">{err}</div>}

            <div className="cap-tabs" role="tablist" aria-label={t("caps.title")}>
              <button
                className={`cap-tab${tab === "servers" ? " cap-tab--active" : ""}`}
                role="tab"
                aria-selected={tab === "servers"}
                onClick={() => setTab("servers")}
              >
                {t("caps.connectorsTab")}
              </button>
              <button
                className={`cap-tab${tab === "skills" ? " cap-tab--active" : ""}`}
                role="tab"
                aria-selected={tab === "skills"}
                onClick={() => setTab("skills")}
              >
                {t("caps.skillsTab")}
              </button>
            </div>

            {tab === "servers" ? (
              <section className="mem-section">
                {serverGroups.failed.length > 0 && (
                  <FailedServersNotice
                    servers={serverGroups.failed}
                    expanded={expandedErrors}
                    onToggle={toggleError}
                    onRetry={(name) => void mutate(() => app.RetryMCPServer(name))}
                    confirming={confirming}
                    onConfirm={setConfirming}
                    onCancelConfirm={() => setConfirming(null)}
                    onRemove={(name) => mutate(() => app.RemoveMCPServer(name)).then(() => setConfirming(null))}
                    busy={busy}
                  />
                )}
                {view.servers.length === 0 && !adding && (
                  <div className="mem-empty">{t("caps.noServers")}</div>
                )}
                <ServerGroup
                  title={t("caps.connectedGroup")}
                  servers={serverGroups.connected}
                  busy={busy}
                  confirming={confirming}
                  onConfirm={setConfirming}
                  onCancelConfirm={() => setConfirming(null)}
                  onRemove={(name) => mutate(() => app.RemoveMCPServer(name)).then(() => setConfirming(null))}
                  onRetry={(name) => void mutate(() => app.RetryMCPServer(name))}
                  onToggle={(name, on) => void mutate(() => app.SetMCPServerEnabled(name, on))}
                />
                <ServerGroup
                  title={t("caps.disabledGroup")}
                  servers={serverGroups.disabled}
                  busy={busy}
                  confirming={confirming}
                  onConfirm={setConfirming}
                  onCancelConfirm={() => setConfirming(null)}
                  onRemove={(name) => mutate(() => app.RemoveMCPServer(name)).then(() => setConfirming(null))}
                  onRetry={(name) => void mutate(() => app.RetryMCPServer(name))}
                  onToggle={(name, on) => void mutate(() => app.SetMCPServerEnabled(name, on))}
                />
                {adding ? (
                  <AddServerForm busy={busy} onCancel={() => setAdding(false)} onAdd={async (input) => (await mutate(() => app.AddMCPServer(input))) && setAdding(false)} />
                ) : (
                  <button className="btn btn--small" disabled={busy} onClick={() => setAdding(true)}>
                    {t("caps.addServer")}
                  </button>
                )}
              </section>
            ) : (
              <section className="mem-section">
                <div className="cap-search">
                  <input
                    className="mem-input"
                    type="search"
                    placeholder={t("caps.searchSkills")}
                    value={skillQuery}
                    onChange={(e) => setSkillQuery(e.target.value)}
                  />
                </div>
                {view.skills.length === 0 ? (
                  <div className="mem-empty">{t("caps.noSkills")}</div>
                ) : filteredSkills.length === 0 ? (
                  <div className="mem-empty">{t("caps.noSkillMatches")}</div>
                ) : (
                  <div className="cap-skills">
                    {filteredSkills.map((sk) => (
                      <SkillRow
                        key={sk.name}
                        skill={sk}
                        expanded={expandedSkills.has(sk.name)}
                        onToggle={() => toggleSkill(sk.name)}
                      />
                    ))}
                  </div>
                )}
              </section>
            )}
          </div>
        )}
    </ResizableDrawer>
  );
}

function ServerGroup({
  title,
  servers,
  busy,
  confirming,
  onConfirm,
  onCancelConfirm,
  onRemove,
  onRetry,
  onToggle,
}: {
  title: string;
  servers: ServerView[];
  busy: boolean;
  confirming: string | null;
  onConfirm: (name: string) => void;
  onCancelConfirm: () => void;
  onRemove: (name: string) => void;
  onRetry: (name: string) => void;
  onToggle: (name: string, on: boolean) => void;
}) {
  if (servers.length === 0) return null;
  return (
    <div className="cap-server-group">
      <div className="cap-server-group__title">
        <span>{title}</span>
        <span>{servers.length}</span>
      </div>
      {servers.map((s) => (
        <ServerRow
          key={s.name}
          s={s}
          busy={busy}
          confirming={confirming === s.name}
          onConfirm={() => onConfirm(s.name)}
          onCancelConfirm={onCancelConfirm}
          onRemove={() => onRemove(s.name)}
          onRetry={() => onRetry(s.name)}
          onToggle={(on) => onToggle(s.name, on)}
        />
      ))}
    </div>
  );
}

function FailedServersNotice({
  servers,
  expanded,
  busy,
  confirming,
  onToggle,
  onRetry,
  onConfirm,
  onCancelConfirm,
  onRemove,
}: {
  servers: ServerView[];
  expanded: Set<string>;
  busy: boolean;
  confirming: string | null;
  onToggle: (name: string) => void;
  onRetry: (name: string) => void;
  onConfirm: (name: string) => void;
  onCancelConfirm: () => void;
  onRemove: (name: string) => void;
}) {
  const t = useT();
  return (
    <div className="cap-failures" role="status">
      <div className="cap-failures__head">
        <div>
          <div className="cap-failures__title">{t("caps.failureTitle", { failed: servers.length })}</div>
          <div className="cap-failures__hint">{t("caps.failureHint")}</div>
        </div>
      </div>
      <div className="cap-failures__list">
        {servers.map((s) => {
          const open = expanded.has(s.name);
          const error = s.error || t("caps.failed");
          return (
            <div className="cap-failure" key={s.name}>
              <div className="cap-failure__main">
                <span className="cap-dot cap-dot--failed" />
                <div className="cap-failure__text">
                  <div className="cap-failure__name">{s.name}</div>
                  <div className="cap-failure__summary">{summarizeServerError(error)}</div>
                </div>
              </div>
              <div className="cap-failure__actions">
                {confirming === s.name ? (
                  <>
                    <button className="btn btn--small" disabled={busy} onClick={() => onRemove(s.name)}>
                      {t("caps.confirmRemove")}
                    </button>
                    <button className="btn btn--small" disabled={busy} onClick={onCancelConfirm}>
                      {t("common.cancel")}
                    </button>
                  </>
                ) : (
                  <>
                    <button className="btn btn--small" disabled={busy} onClick={() => onRetry(s.name)}>
                      {t("caps.retry")}
                    </button>
                    <button className="btn btn--small" onClick={() => void navigator.clipboard?.writeText(error)}>
                      {t("common.copy")}
                    </button>
                    <button className="btn btn--small" onClick={() => onToggle(s.name)} aria-expanded={open}>
                      {open ? t("common.collapse") : t("caps.showLog")}
                    </button>
                    <button className="btn btn--small" disabled={busy} onClick={() => onConfirm(s.name)} title={t("caps.remove")}>
                      ✕
                    </button>
                  </>
                )}
              </div>
              {open && <pre className="cap-failure__log">{error}</pre>}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function ServerRow({
  s,
  busy,
  confirming,
  onConfirm,
  onCancelConfirm,
  onRemove,
  onRetry,
  onToggle,
}: {
  s: ServerView;
  busy: boolean;
  confirming: boolean;
  onConfirm: () => void;
  onCancelConfirm: () => void;
  onRemove: () => void;
  onRetry: () => void;
  onToggle: (on: boolean) => void;
}) {
  const t = useT();
  const actionLabel = serverActionLabel(s, t);
  const sub =
    s.status === "failed"
      ? s.error || t("caps.failed")
      : s.status === "disabled"
        ? t("caps.disabled")
        : t("caps.counts", { tools: s.tools, prompts: s.prompts, resources: s.resources });
  return (
    <div className="cap-row" title={s.error || undefined}>
      <span className={`cap-dot cap-dot--${s.status}`} />
      <div className="cap-row__text">
        <div className="cap-row__head">
          <span className="cap-row__name">{s.name}</span>
          <span className="cap-row__transport">{s.transport}</span>
        </div>
        <div className="cap-row__sub">{sub}</div>
      </div>
      <div className="cap-row__actions">
        {confirming ? (
          <>
            <button className="btn btn--small" disabled={busy} onClick={onRemove}>
              {t("caps.confirmRemove")}
            </button>
            <button className="btn btn--small" disabled={busy} onClick={onCancelConfirm}>
              {t("common.cancel")}
            </button>
          </>
        ) : (
          <>
            {s.status === "failed" ? (
              <button className="btn btn--small" disabled={busy} onClick={onRetry}>
                {actionLabel}
              </button>
            ) : (
              <label className="cap-switch" title={s.status === "connected" ? t("caps.disable") : t("caps.enable")}>
                <input
                  type="checkbox"
                  checked={s.status === "connected"}
                  disabled={busy}
                  onChange={(e) => onToggle(e.target.checked)}
                />
                <span className="cap-switch__track" />
              </label>
            )}
            <button className="btn btn--small" disabled={busy} onClick={onConfirm} title={t("caps.remove")}>
              ✕
            </button>
          </>
        )}
      </div>
    </div>
  );
}

function summarizeServerError(error: string): string {
  const normalized = error.replace(/\s+/g, " ").trim();
  const plugin = normalized.match(/plugin "([^"]+)"/i)?.[1];
  const npmCode = normalized.match(/\bnpm error code ([A-Z0-9_]+)/i)?.[1];
  const errno = normalized.match(/\berrno (-?\d+)/i)?.[1];
  const reason = npmCode
    ? `npm ${npmCode}${errno ? ` (${errno})` : ""}`
    : normalized.split(/(?:\.\s+|\n)/)[0];
  const summary = plugin ? `${plugin}: ${reason}` : reason;
  return summary.length > 180 ? `${summary.slice(0, 176).trim()}…` : summary;
}

function serverActionLabel(s: ServerView, t: ReturnType<typeof useT>): string {
  const err = (s.error || "").toLowerCase();
  if (err.includes("401") || err.includes("unauthorized")) return t("caps.reauthorize");
  if (
    err.includes("command not found") ||
    err.includes("executable file not found") ||
    err.includes("no such file") ||
    err.includes("enoent")
  ) {
    return t("caps.checkCommand");
  }
  return t("caps.retry");
}

function SkillRow({
  skill,
  expanded,
  onToggle,
}: {
  skill: SkillView;
  expanded: boolean;
  onToggle: () => void;
}) {
  const t = useT();
  const summary = summarizeSkillDescription(skill.description);
  const canExpand = summary !== skill.description;
  return (
    <button
      className={`cap-skill-card${expanded ? " cap-skill-card--expanded" : ""}${canExpand ? " cap-skill-card--expandable" : ""}`}
      type="button"
      onClick={onToggle}
      aria-expanded={expanded}
      title={skill.description}
    >
      <div className="cap-skill-card__head">
        <span className="cap-skill-card__icon">/</span>
        <span className="cap-skill-card__main">
          <span className="cap-skill-card__command">{skill.name}</span>
          <span className="cap-skill-card__badges">
            <span className={`badge badge--${skill.scope}`}>{skill.scope}</span>
            {skill.runAs === "subagent" && <span className="badge">{t("caps.subagent")}</span>}
          </span>
        </span>
      </div>
      <div className="cap-skill-card__desc">{expanded ? skill.description : summary}</div>
      {canExpand && <div className="cap-skill-card__more">{expanded ? t("common.collapse") : t("common.expand")}</div>}
    </button>
  );
}

function summarizeSkillDescription(description: string): string {
  const normalized = description.replace(/\s+/g, " ").trim();
  if (normalized.length <= 132) return normalized;
  const sentence = normalized.match(/^.{48,132}?[。.!?；;，,]/u)?.[0]?.trim();
  if (sentence && sentence.length >= 48) return sentence.replace(/[。.!?；;，,]$/u, "");
  return `${normalized.slice(0, 128).trim()}…`;
}

function AddServerForm({
  busy,
  onCancel,
  onAdd,
}: {
  busy: boolean;
  onCancel: () => void;
  onAdd: (input: MCPServerInput) => void;
}) {
  const t = useT();
  const [name, setName] = useState("");
  const [transport, setTransport] = useState("stdio");
  const [command, setCommand] = useState("");
  const [url, setUrl] = useState("");
  const [env, setEnv] = useState("");

  const isStdio = transport === "stdio";
  const ready = name.trim() !== "" && (isStdio ? command.trim() !== "" : url.trim() !== "");

  const submit = () => {
    const parts = command.trim().split(/\s+/).filter(Boolean);
    const envMap: Record<string, string> = {};
    for (const line of env.split("\n")) {
      const eq = line.indexOf("=");
      if (eq > 0) envMap[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
    }
    onAdd({
      name: name.trim(),
      transport,
      command: isStdio ? (parts[0] ?? "") : "",
      args: isStdio ? parts.slice(1) : [],
      url: isStdio ? "" : url.trim(),
      env: envMap,
    });
  };

  return (
    <div className="prov-card prov-card--edit">
      <input className="mem-input" placeholder={t("caps.namePlaceholder")} value={name} onChange={(e) => setName(e.target.value)} />
      <label className="set-label">{t("caps.transport")}</label>
      <select className="mem-select" value={transport} onChange={(e) => setTransport(e.target.value)}>
        <option value="stdio">stdio</option>
        <option value="http">http</option>
        <option value="sse">sse</option>
      </select>
      {isStdio ? (
        <input className="mem-input" placeholder={t("caps.commandPlaceholder")} value={command} onChange={(e) => setCommand(e.target.value)} />
      ) : (
        <input className="mem-input" placeholder={t("caps.urlPlaceholder")} value={url} onChange={(e) => setUrl(e.target.value)} />
      )}
      <label className="set-label">{t("caps.envLabel")}</label>
      <textarea className="mem-textarea" value={env} onChange={(e) => setEnv(e.target.value)} placeholder={t("caps.envPlaceholder")} spellCheck={false} />
      <div className="prov-card__actions">
        <button className="btn btn--small" onClick={onCancel} disabled={busy}>
          {t("common.cancel")}
        </button>
        <button className="btn btn--primary btn--small" onClick={submit} disabled={busy || !ready}>
          {t("caps.add")}
        </button>
      </div>
    </div>
  );
}
