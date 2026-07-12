import { Check, Monitor, Server, X } from "lucide-react";
import { useEffect, useId, useState } from "react";
import type { SessionRuntime } from "../protocol/types";
import { t, type Locale } from "../i18n/messages";

const DEFAULT_NODE = "http://127.0.0.1:8790";

export function NewSessionSheet({
  open,
  locale,
  busy,
  error,
  onClose,
  onCreate,
}: {
  open: boolean;
  locale: Locale;
  busy: boolean;
  error: string | null;
  onClose: () => void;
  onCreate: (input: { runtime: SessionRuntime; nodeUrl?: string }) => void;
}) {
  const titleId = useId();
  const [runtime, setRuntime] = useState<SessionRuntime>("local");
  const [nodeUrl, setNodeUrl] = useState(DEFAULT_NODE);

  useEffect(() => {
    if (!open) return;
    setRuntime("local");
    setNodeUrl(DEFAULT_NODE);
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div className="sheet-root" role="dialog" aria-modal="true" aria-labelledby={titleId}>
      <button
        type="button"
        className="sheet-backdrop"
        aria-label={t(locale, "common.close")}
        onClick={onClose}
      />
      <div className="sheet-panel">
        <div className="sheet-handle" aria-hidden />
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start" }}>
          <h2 id={titleId} className="sheet-title">
            {t(locale, "sessions.pickRuntime")}
          </h2>
          <button
            type="button"
            className="icon-btn neutral"
            style={{ marginRight: 8 }}
            aria-label={t(locale, "common.close")}
            onClick={onClose}
          >
            <X size={20} />
          </button>
        </div>
        <p className="sheet-desc">{t(locale, "sessions.pickRuntimeDesc")}</p>

        <button
          type="button"
          className="choice-card"
          aria-pressed={runtime === "local"}
          onClick={() => setRuntime("local")}
        >
          <span className="choice-icon" aria-hidden>
            <Monitor size={20} />
          </span>
          <span>
            <div className="choice-title">{t(locale, "sessions.runtimeLocal")}</div>
            <div className="choice-desc">{t(locale, "sessions.localDesc")}</div>
          </span>
          {runtime === "local" ? <Check size={18} color="var(--rx-accent)" /> : <span />}
        </button>

        <button
          type="button"
          className="choice-card"
          aria-pressed={runtime === "remote"}
          onClick={() => setRuntime("remote")}
        >
          <span className="choice-icon remote" aria-hidden>
            <Server size={20} />
          </span>
          <span>
            <div className="choice-title">{t(locale, "sessions.runtimeRemote")}</div>
            <div className="choice-desc">{t(locale, "sessions.remoteDesc")}</div>
          </span>
          {runtime === "remote" ? <Check size={18} color="var(--rx-accent)" /> : <span />}
        </button>

        {runtime === "remote" && (
          <div className="sheet-field">
            <label htmlFor="node-url">{t(locale, "sessions.nodeUrl")}</label>
            <input
              id="node-url"
              value={nodeUrl}
              onChange={(e) => setNodeUrl(e.target.value)}
              placeholder={t(locale, "sessions.nodeUrlPlaceholder")}
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
              inputMode="url"
            />
          </div>
        )}

        {error ? <p className="sheet-error">{error}</p> : null}

        <div className="sheet-actions">
          <button
            type="button"
            className="btn-primary"
            disabled={busy || (runtime === "remote" && !nodeUrl.trim())}
            onClick={() =>
              onCreate({
                runtime,
                nodeUrl: runtime === "remote" ? nodeUrl.trim() : undefined,
              })
            }
          >
            {t(locale, "sessions.create")}
          </button>
          <button type="button" className="btn-secondary" onClick={onClose} disabled={busy}>
            {t(locale, "sessions.cancel")}
          </button>
        </div>
      </div>
    </div>
  );
}
