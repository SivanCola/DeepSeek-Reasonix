// ContextPanel shows the active tab's context gauge, token usage, read files,
// and workspace changes. It replaces/extend the right-hand workspace panel's
// metadata view.
import { useCallback, useEffect, useState } from "react";
import { FileText, X } from "lucide-react";
import { asArray } from "../lib/array";
import { app } from "../lib/bridge";
import type { ContextInfo, ContextPanelInfo, WireUsage } from "../lib/types";

interface ContextPanelProps {
  tabId?: string;
  context?: ContextInfo;
  usage?: WireUsage;
  sessionCostUsd?: number;
  scopeLabel?: string;
  onClose?: () => void;
}

type ContextTab = "usage" | "read" | "changed";

function fmtTokens(n: number): string {
  if (n >= 1000) return `${Math.round(n / 1000)}k`;
  return String(n);
}

function fmtTime(ms?: number): string {
  if (!ms) return "";
  return new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function fmtDuration(ms: number): string {
  if (ms <= 0) return "-";
  const totalSeconds = Math.max(1, Math.round(ms / 1000));
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  if (minutes <= 0) return `${seconds}s`;
  return `${minutes}m ${seconds}s`;
}

export function ContextPanel({ tabId, context, usage, sessionCostUsd, scopeLabel, onClose }: ContextPanelProps) {
  const [info, setInfo] = useState<ContextPanelInfo | null>(null);
  const [tab, setTab] = useState<ContextTab>("usage");

  const refresh = useCallback(async () => {
    if (!tabId) return;
    try {
      setInfo(await app.ContextPanel(tabId));
    } catch {
      /* bridge unavailable */
    }
  }, [tabId]);

  useEffect(() => {
    void refresh();
    const id = window.setInterval(() => void refresh(), 2000);
    return () => window.clearInterval(id);
  }, [refresh]);

  const usedTokens = context?.used && context.used > 0 ? context.used : info?.usedTokens ?? 0;
  const windowTokens = context?.window && context.window > 0 ? context.window : info?.windowTokens ?? 0;
  const promptTokens = usage?.promptTokens && usage.promptTokens > 0 ? usage.promptTokens : info?.promptTokens ?? 0;
  const completionTokens = usage?.completionTokens && usage.completionTokens > 0 ? usage.completionTokens : info?.completionTokens ?? 0;
  const reasoningTokens = usage?.reasoningTokens && usage.reasoningTokens > 0 ? usage.reasoningTokens : info?.reasoningTokens ?? 0;
  const cacheHitTokens = usage?.cacheHitTokens && usage.cacheHitTokens > 0 ? usage.cacheHitTokens : info?.cacheHitTokens ?? 0;
  const cacheMissTokens = usage?.cacheMissTokens && usage.cacheMissTokens > 0 ? usage.cacheMissTokens : info?.cacheMissTokens ?? 0;
  const cost = sessionCostUsd && sessionCostUsd > 0 ? sessionCostUsd : info?.sessionCostUsd ?? 0;
  const readFiles = asArray(info?.readFiles);
  const changedFiles = asArray(info?.changedFiles);

  const usagePct = windowTokens > 0 ? Math.round((usedTokens / windowTokens) * 100) : 0;
  const cachePct = cacheHitTokens + cacheMissTokens > 0
    ? Math.round((cacheHitTokens / (cacheHitTokens + cacheMissTokens)) * 100)
    : 0;
  const otherTokens = Math.max(0, usedTokens - promptTokens - completionTokens - reasoningTokens);
  const safeUsed = Math.max(usedTokens, 1);
  const promptPct = Math.min(100, (promptTokens / safeUsed) * usagePct);
  const completionPct = Math.min(100, promptPct + (completionTokens / safeUsed) * usagePct);
  const reasoningPct = Math.min(100, completionPct + (reasoningTokens / safeUsed) * usagePct);
  const otherPct = Math.min(100, reasoningPct + (otherTokens / safeUsed) * usagePct);
  const donutStyle = {
    background: `conic-gradient(#13a7a5 0 ${promptPct}%, #2f6df6 ${promptPct}% ${completionPct}%, #f97316 ${completionPct}% ${reasoningPct}%, var(--border) ${reasoningPct}% ${otherPct}%, var(--border-soft) ${otherPct}% 100%)`,
  };
  const eventTimes = [
    ...readFiles.map((file) => file.time),
    ...changedFiles.map((file) => file.latestTime ?? 0),
  ].filter((time) => time > 0);
  const elapsed = eventTimes.length > 1 ? Math.max(...eventTimes) - Math.min(...eventTimes) : 0;
  const requestCount = Math.max(readFiles.length + changedFiles.length, 0);
  const readRows = readFiles.map((f, i) => ({
    key: `${f.path}-${i}`,
    path: f.path,
    meta: `#${f.turn}`,
    time: fmtTime(f.time),
    detail: f.limit ? `${f.offset ?? 0}-${(f.offset ?? 0) + f.limit}${f.truncated ? " truncated" : ""}` : "",
  }));
  const changedRows = changedFiles.map((f, i) => ({
    key: `${f.path}-${i}`,
    path: f.path,
    meta: f.gitStatus || asArray(f.sources).join(", ") || "changed",
    time: fmtTime(f.latestTime),
    detail: asArray(f.turns).length > 0 ? `T${asArray(f.turns).join(",")}` : "",
  }));

  return (
    <div className="context-panel">
      <header className="context-panel__head">
        <div className="context-panel__title">当前主题上下文</div>
        {onClose && (
          <button className="context-panel__close" onClick={onClose} aria-label="关闭上下文面板">
            <X size={15} />
          </button>
        )}
      </header>

      <div className="context-panel__tabs" role="tablist" aria-label="当前主题上下文">
        <button className={`context-panel__tab${tab === "usage" ? " context-panel__tab--active" : ""}`} onClick={() => setTab("usage")}>Usage</button>
        <button className={`context-panel__tab${tab === "read" ? " context-panel__tab--active" : ""}`} onClick={() => setTab("read")}>Read Files</button>
        <button className={`context-panel__tab${tab === "changed" ? " context-panel__tab--active" : ""}`} onClick={() => setTab("changed")}>Changed</button>
      </div>

      <div className="context-panel__body">
        {tab === "usage" && (
          <section className="context-panel__usage">
            <div className="context-panel__donut" style={donutStyle}>
              <div className="context-panel__donut-core">
                <strong>{fmtTokens(usedTokens)}</strong>
                <span>/ {fmtTokens(windowTokens)} tokens</span>
              </div>
            </div>
            <div className="context-panel__percent">{usagePct}%</div>
            <div className="context-panel__breakdown">
              <TokenLegend label="Prompt" value={promptTokens} color="prompt" />
              <TokenLegend label="Completion" value={completionTokens} color="completion" />
              <TokenLegend label="Reasoning" value={reasoningTokens} color="reasoning" />
              <TokenLegend label="Other" value={otherTokens} color="other" />
              <div className="context-panel__total">
                <span>Total</span>
                <strong>{usedTokens.toLocaleString()} / {windowTokens.toLocaleString()}</strong>
              </div>
            </div>
            <div className="context-panel__stats">
              <MetricCard label="Cache hit" value={cachePct > 0 ? `${cachePct}%` : "-"} tone="accent" />
              <MetricCard label="Session cost" value={cost > 0 ? `¥${cost < 1 ? cost.toFixed(3) : cost.toFixed(2)}` : "-"} />
              <MetricCard label="Requests" value={requestCount > 0 ? String(requestCount) : "-"} />
              <MetricCard label="Time" value={fmtDuration(elapsed)} />
            </div>
            <PreviewSection
              title="Read Files"
              action="View all"
              onAction={() => setTab("read")}
              rows={readRows.slice(0, 4)}
              empty="还没有读取文件"
            />
            <PreviewSection
              title={`Changed Files${changedFiles.length > 0 ? ` (${changedFiles.length})` : ""}`}
              action="View all"
              onAction={() => setTab("changed")}
              rows={changedRows.slice(0, 3)}
              empty="还没有变更文件"
            />
          </section>
        )}

        {tab === "read" && (
          <FileTable
            empty="还没有读取文件"
            rows={readRows}
          />
        )}

        {tab === "changed" && (
          <FileTable
            empty="还没有变更文件"
            rows={changedRows}
          />
        )}
      </div>

      <footer className="context-panel__scope">
        <FileText size={14} />
        <span>{scopeLabel || "Scope: Global"}</span>
      </footer>
    </div>
  );
}

function PreviewSection({
  title,
  action,
  onAction,
  rows,
  empty,
}: {
  title: string;
  action: string;
  onAction: () => void;
  rows: Array<{ key: string; path: string; meta: string; time: string; detail: string }>;
  empty: string;
}) {
  return (
    <section className="context-panel__preview">
      <header className="context-panel__preview-head">
        <h3>{title}</h3>
        {rows.length > 0 && <button onClick={onAction}>{action}</button>}
      </header>
      <FileTable rows={rows} empty={empty} compact />
    </section>
  );
}

function TokenLegend({ label, value, color }: { label: string; value: number; color: string }) {
  return (
    <div className="context-panel__legend-row">
      <span className={`context-panel__legend-dot context-panel__legend-dot--${color}`} />
      <span>{label}</span>
      <strong>{value.toLocaleString()}</strong>
    </div>
  );
}

function MetricCard({ label, value, tone }: { label: string; value: string; tone?: "accent" }) {
  return (
    <div className="context-panel__metric">
      <span>{label}</span>
      <strong className={tone === "accent" ? "context-panel__metric-accent" : ""}>{value}</strong>
    </div>
  );
}

function FileTable({
  rows,
  empty,
  compact = false,
}: {
  rows: Array<{ key: string; path: string; meta: string; time: string; detail: string }>;
  empty: string;
  compact?: boolean;
}) {
  if (rows.length === 0) return <div className="context-panel__empty">{empty}</div>;
  return (
    <div className={`context-panel__table${compact ? " context-panel__table--compact" : ""}`}>
      <div className="context-panel__table-head">
        <span>文件</span>
        <span>Turn</span>
        <span>时间</span>
      </div>
      {rows.map((row) => (
        <div className="context-panel__table-row" key={row.key}>
          <span className="context-panel__table-file">
            <FileText size={14} />
            <span>{row.path}</span>
            {row.detail && <small>{row.detail}</small>}
          </span>
          <span>{row.meta}</span>
          <span>{row.time}</span>
        </div>
      ))}
    </div>
  );
}
