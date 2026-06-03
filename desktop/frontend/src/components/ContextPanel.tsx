// ContextPanel shows the active tab's context gauge, token usage, read files,
// and workspace changes. It replaces/extend the right-hand workspace panel's
// metadata view.
import { useCallback, useEffect, useState } from "react";
import { ArrowLeft, FileText, Search } from "lucide-react";
import { asArray } from "../lib/array";
import { app } from "../lib/bridge";
import type { ContextInfo, ContextPanelInfo, WireUsage } from "../lib/types";

interface ContextPanelProps {
  tabId?: string;
  context?: ContextInfo;
  usage?: WireUsage;
  sessionCostUsd?: number;
  scopeLabel?: string;
  refreshKey?: number;
}

type ContextDetail = "read" | "changed";

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

function contextHealth(usagePct: number, cachePct: number, readCount: number) {
  if (usagePct >= 85) {
    return {
      tone: "warn",
      label: "上下文接近上限",
      body: `已使用 ${usagePct}%，优先保留关键依据并考虑压缩。`,
    };
  }
  if (readCount >= 8) {
    return {
      tone: "notice",
      label: "依据文件较多",
      body: `已读取 ${readCount} 条文件记录，可查看依据文件确认是否仍相关。`,
    };
  }
  if (cachePct > 0 && cachePct < 50) {
    return {
      tone: "notice",
      label: "缓存命中偏低",
      body: `当前缓存命中 ${cachePct}%，后续长任务可关注上下文稳定性。`,
    };
  }
  return {
    tone: "good",
    label: "上下文状态正常",
    body: "用量、依据文件和本主题变更保持可追踪。",
  };
}

export function ContextPanel({ tabId, context, usage, sessionCostUsd, scopeLabel, refreshKey }: ContextPanelProps) {
  const [info, setInfo] = useState<ContextPanelInfo | null>(null);
  const [detailView, setDetailView] = useState<ContextDetail | null>(null);
  const [query, setQuery] = useState("");

  const refresh = useCallback(async () => {
    if (!tabId) return;
    try {
      setInfo(await app.ContextPanel(tabId));
    } catch {
      /* bridge unavailable */
    }
  }, [tabId]);

  useEffect(() => {
    const id = window.setInterval(() => void refresh(), 2000);
    return () => window.clearInterval(id);
  }, [refresh]);

  useEffect(() => {
    void refresh();
  }, [refresh, refreshKey]);

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
  const normalizedQuery = query.trim().toLowerCase();
  const filterRows = (rows: typeof readRows) => {
    if (!normalizedQuery) return rows;
    return rows.filter((row) =>
      `${row.path} ${row.meta} ${row.time} ${row.detail}`.toLowerCase().includes(normalizedQuery)
    );
  };
  const filteredReadRows = filterRows(readRows);
  const filteredChangedRows = filterRows(changedRows);
  const health = contextHealth(usagePct, cachePct, readRows.length);
  const detailRows = detailView === "changed" ? filteredChangedRows : filteredReadRows;
  const detailTitle = detailView === "changed" ? "本主题变更" : "依据文件";
  const detailCount = detailView === "changed" ? changedRows.length : readRows.length;
  const detailEmpty = detailView === "changed" ? "当前主题还没有变更文件" : "当前主题还没有读取文件";
  const detailPlaceholder = detailView === "changed" ? "筛选本主题变更文件..." : "筛选依据文件...";
  const detailNote = detailView === "changed"
    ? `当前主题关联 ${detailCount} 条变更记录`
    : `当前主题读取过 ${detailCount} 条文件记录`;

  const openDetail = (next: ContextDetail) => {
    setDetailView(next);
    setQuery("");
  };

  const closeDetail = () => {
    setDetailView(null);
    setQuery("");
  };

  return (
    <div className="context-panel">
      <div className="context-panel__summary-head">
        <div className="context-panel__heading-main">
          <span>{detailView ? detailTitle : "当前主题概览"}</span>
          <strong>{scopeLabel || "范围：全局"}</strong>
        </div>
        {detailView && (
          <button className="context-panel__back" type="button" onClick={closeDetail}>
            <ArrowLeft size={13} />
            概览
          </button>
        )}
      </div>

      {detailView && (
        <label className="context-panel__search">
          <Search size={14} />
          <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={detailPlaceholder} />
        </label>
      )}

      <div className="context-panel__body">
        {detailView ? (
          <section className="context-panel__detail">
            <div className="context-panel__detail-note">{detailNote}</div>
            <FileTable
              empty={detailEmpty}
              rows={detailRows}
            />
          </section>
        ) : (
          <section className="context-panel__overview">
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
            </section>
            <div className={`context-panel__health context-panel__health--${health.tone}`}>
              <span>上下文状态</span>
              <strong>{health.label}</strong>
              <small>{health.body}</small>
            </div>
            <PreviewSection
              title="依据文件"
              meta={`${readRows.length} 条读取记录`}
              action="查看全部"
              onAction={() => openDetail("read")}
              rows={readRows.slice(0, 3)}
              empty="当前主题还没有读取文件"
            />
            <PreviewSection
              title="本主题变更"
              meta={`${changedRows.length} 条变更记录`}
              action="查看全部"
              onAction={() => openDetail("changed")}
              rows={changedRows.slice(0, 3)}
              empty="当前主题还没有变更文件"
            />
          </section>
        )}
      </div>

      <footer className="context-panel__scope">
        <FileText size={14} />
        <span>{scopeLabel || "范围：全局"}</span>
      </footer>
    </div>
  );
}

function PreviewSection({
  title,
  meta,
  action,
  onAction,
  rows,
  empty,
}: {
  title: string;
  meta?: string;
  action: string;
  onAction: () => void;
  rows: Array<{ key: string; path: string; meta: string; time: string; detail: string }>;
  empty: string;
}) {
  return (
    <section className="context-panel__preview">
      <header className="context-panel__preview-head">
        <h3>{title}</h3>
        {meta && <span>{meta}</span>}
        {rows.length > 0 && <button type="button" onClick={onAction}>{action}</button>}
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
    <div className={`context-panel__file-list${compact ? " context-panel__file-list--compact" : ""}`}>
      {rows.map((row) => (
        <div className="context-panel__file-row" key={row.key}>
          <span className="context-panel__file-main">
            <FileText size={14} />
            <span className="context-panel__file-copy">
              <span>{row.path}</span>
              {row.detail && <small>{row.detail}</small>}
            </span>
          </span>
          <span className="context-panel__file-meta">
            <span className="context-panel__file-turn">{row.meta}</span>
            {row.time && <span>{row.time}</span>}
          </span>
        </div>
      ))}
    </div>
  );
}
