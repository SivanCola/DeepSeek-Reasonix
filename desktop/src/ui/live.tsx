import { useEffect, useRef, useState } from "react";
import { t, useLang } from "../i18n";
import { I } from "../icons";

function hashString(value: string): string {
  let hash = 0;
  for (let i = 0; i < value.length; i++) {
    hash = (hash * 31 + value.charCodeAt(i)) | 0;
  }
  return `${value.length}-${(hash >>> 0).toString(36)}`;
}

function keyed<T>(items: readonly T[], keyFor: (item: T) => string): { item: T; key: string }[] {
  const seen = new Map<string, number>();
  return items.map((item) => {
    const base = keyFor(item);
    const count = seen.get(base) ?? 0;
    seen.set(base, count + 1);
    return { item, key: count === 0 ? base : `${base}-${count}` };
  });
}

export function fmtElapsed(ms: number): string {
  const s = ms / 1000;
  return s < 10 ? `${s.toFixed(1)}s` : `${Math.floor(s)}s`;
}

export function useElapsed(active: boolean, startAt?: number): number {
  const [ms, setMs] = useState(0);
  const start = useRef<number | null>(null);
  useEffect(() => {
    if (!active) {
      setMs(0);
      start.current = null;
      return;
    }
    start.current = startAt ?? performance.now();
    const id = setInterval(() => {
      if (start.current !== null) setMs(performance.now() - start.current);
    }, 80);
    return () => clearInterval(id);
  }, [active, startAt]);
  return ms;
}

export function ThinkingPill({
  phase = "thinking",
  label,
  elapsedMs,
}: {
  phase?: "queued" | "thinking" | "tool";
  label: string;
  elapsedMs: number;
}) {
  const color =
    phase === "queued" ? "var(--muted)" : phase === "tool" ? "var(--warning)" : "var(--accent)";
  return (
    <div className="thinking">
      <span className="dots" style={{ color }}>
        <span style={{ background: color }} />
        <span style={{ background: color }} />
        <span style={{ background: color }} />
      </span>
      <span className="label">
        <span className="sh">{label}</span>
      </span>
      <span className="timer">{fmtElapsed(elapsedMs)}</span>
    </div>
  );
}

export function LiveReasoning({ lines }: { lines: string[] }) {
  useLang();
  const keyedLines = keyed(lines, hashString);
  return (
    <div className="live-reason">
      <div className="head">
        <span className="dot" /> {t("live.reasoning")}
      </div>
      {keyedLines.map(({ item: line, key }, i) => (
        <div key={key}>
          {line}
          {i === lines.length - 1 ? <span className="stream-caret" /> : null}
        </div>
      ))}
    </div>
  );
}

export function ToolRunningCard({
  kind = "tool",
  name,
  elapsedMs,
  logLines,
}: {
  kind?: "shell" | "fetch" | "search" | "tool";
  name: string;
  elapsedMs: number;
  logLines?: { text: string; tone?: "ok" | "dim" }[];
}) {
  useLang();
  const keyedLogLines = logLines
    ? keyed(logLines, (line) => `${hashString(line.text)}-${line.tone ?? ""}`)
    : [];
  const ic =
    kind === "shell" ? (
      <I.terminal size={12} />
    ) : kind === "fetch" ? (
      <I.globe size={12} />
    ) : kind === "search" ? (
      <I.search size={12} />
    ) : (
      <I.wrench size={12} />
    );
  return (
    <div className="skel-card">
      <div className="h">
        <span className="ico">{ic}</span>
        <span className="kind">{kind}</span>
        <span style={{ color: "var(--fg)", fontWeight: 500 }}>{name}</span>
        <span className="grow" />
        <span
          className="spin-meta"
          role="img"
          aria-label={t("live.running")}
          title={t("live.running")}
        />
        <span className="timer">{fmtElapsed(elapsedMs)}</span>
      </div>
      {logLines && logLines.length > 0 ? (
        <div className="live-log">
          {keyedLogLines.map(({ item: ln, key }, i) => (
            <div
              key={key}
              className={`line ${ln.tone ?? ""}`}
              style={{ animationDelay: `${i * 0.25}s` }}
            >
              {ln.text}
            </div>
          ))}
        </div>
      ) : (
        <div className="body">
          <div className="skel-line w-90" />
          <div className="skel-line w-70" />
          <div className="skel-line w-60" />
        </div>
      )}
    </div>
  );
}

export function PendingUserMsg({ text, elapsedMs }: { text: string; elapsedMs: number }) {
  useLang();
  return (
    <div className="msg user">
      <div className="avatar">YOU</div>
      <div className="body">
        <div className="who">
          <span className="name">{t("live.you")}</span>
          <span className="time">
            {t("live.secondsAgo", { seconds: (elapsedMs / 1000).toFixed(1) })}
          </span>
        </div>
        <div className="msg-text user-pending">{text}</div>
        <div className="user-status">
          <span className="spin" />
          <span>{t("live.deliveredWaiting")}</span>
        </div>
      </div>
    </div>
  );
}
