import { ArrowLeft, ArrowUp } from "lucide-react";
import { useEffect, useRef } from "react";
import type { SessionDescriptor } from "../protocol/types";
import { t, type Locale } from "../i18n/messages";
import { IconButton } from "./IconButton";
import { TopBar } from "./TopBar";
import { useKeyboardInset } from "../lib/useKeyboardInset";

export interface ChatLine {
  id: string;
  kind: string;
  text: string;
  role?: "user" | "assistant" | "system" | "tool";
}

function lineRole(line: ChatLine): ChatLine["role"] {
  if (line.role) return line.role;
  if (line.kind === "user") return "user";
  if (
    line.kind === "tool_dispatch" ||
    line.kind === "tool_result" ||
    line.kind === "tool_progress" ||
    line.kind === "notice"
  ) {
    return "tool";
  }
  if (line.kind === "turn_started" || line.kind === "turn_done") return "system";
  return "assistant";
}

export function ChatView({
  session,
  lines,
  draft,
  sending,
  locale,
  showBack,
  onBack,
  onDraftChange,
  onSend,
}: {
  session: SessionDescriptor;
  lines: ChatLine[];
  draft: string;
  sending: boolean;
  locale: Locale;
  showBack: boolean;
  onBack: () => void;
  onDraftChange: (v: string) => void;
  onSend: () => void;
}) {
  const streamRef = useRef<HTMLDivElement>(null);
  const keyboardInset = useKeyboardInset();
  const subtitle =
    session.runtime === "local"
      ? t(locale, "sessions.runtimeLocal")
      : t(locale, "sessions.runtimeRemote");

  useEffect(() => {
    const el = streamRef.current;
    if (!el) return;
    el.scrollTop = el.scrollHeight;
  }, [lines, keyboardInset]);

  return (
    <div
      className="chat-shell"
      style={{ ["--rx-keyboard-inset" as string]: `${keyboardInset}px` }}
    >
      <TopBar
        title={session.title || session.id}
        subtitle={subtitle}
        leading={
          showBack ? (
            <IconButton label={t(locale, "common.back")} onClick={onBack} neutral>
              <ArrowLeft size={22} strokeWidth={2} />
            </IconButton>
          ) : (
            <span style={{ width: 44 }} />
          )
        }
      />
      <div className="chat-stream" ref={streamRef} aria-live="polite">
        {lines.length === 0 ? (
          <p className="chat-empty">{t(locale, "sessions.chatEmpty")}</p>
        ) : (
          lines.map((line) => {
            const role = lineRole(line);
            if (role === "tool") {
              return (
                <div
                  key={line.id}
                  className="tool-timeline"
                  data-kind={line.kind}
                >
                  {line.text}
                </div>
              );
            }
            return (
              <div key={line.id} className="bubble" data-role={role}>
                {line.text}
              </div>
            );
          })
        )}
      </div>
      <div className="composer-dock">
        <div className="composer-row">
          <textarea
            className="composer-field"
            value={draft}
            rows={1}
            onChange={(e) => onDraftChange(e.target.value)}
            placeholder={t(locale, "composer.placeholder")}
            aria-label={t(locale, "composer.placeholder")}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                onSend();
              }
            }}
          />
          <button
            type="button"
            className="composer-send"
            onClick={onSend}
            disabled={sending || !draft.trim()}
            aria-label={t(locale, "composer.send")}
          >
            <ArrowUp size={20} strokeWidth={2.5} />
          </button>
        </div>
      </div>
    </div>
  );
}
