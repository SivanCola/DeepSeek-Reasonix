import { memo, useState } from "react";
import { ChevronRight, MoreHorizontal } from "lucide-react";
import { Markdown } from "./Markdown";
import { CopyButton } from "./CopyButton";
import { Tooltip } from "./Tooltip";
import { useT } from "../lib/i18n";
import type { Item } from "../lib/useController";

type AssistantItem = Extract<Item, { kind: "assistant" }>;

export function UserMessage({
  text,
  turn,
  anchorId,
  open,
  onToggle,
  onRewind,
  rewindDisabled = false,
}: {
  text: string;
  turn?: number;
  anchorId?: string;
  open?: boolean; // whether this message's rewind menu is the open one (lifted to Transcript)
  onToggle?: () => void;
  onRewind?: (turn: number, scope: string) => void;
  rewindDisabled?: boolean;
}) {
  const t = useT();
  const [confirmScope, setConfirmScope] = useState<string | null>(null);
  const canRewind = onRewind != null && turn != null;
  const rewind = (scope: string) => {
    setConfirmScope(null);
    onRewind?.(turn as number, scope);
  };
  const selectRewind = (scope: string) => {
    if (rewindDisabled) return;
    if (scope === "both" || scope === "conversation" || scope === "code") {
      if (confirmScope !== scope) {
        setConfirmScope(scope);
        return;
      }
    }
    rewind(scope);
  };
  const displayText = text.replace(/@\.reasonix\/attachments\/[^\s]+/g, "[image]");
  return (
    <div className="msg msg--user" id={anchorId} data-question-anchor={anchorId} data-turn={turn}>
      <span className="msg__caret">›</span>
      <div className="msg__text">{displayText}</div>
      {canRewind && (
        <div className={`rewind${open ? " rewind--open" : ""}`}>
          <Tooltip label={t("rewind.label")}>
            <button
              className="rewind__btn"
              type="button"
              aria-label={t("rewind.label")}
              aria-expanded={Boolean(open)}
              onClick={() => {
                setConfirmScope(null);
                onToggle?.();
              }}
            >
              <MoreHorizontal size={15} />
            </button>
          </Tooltip>
          {open && (
            <div className="rewind__menu">
              <div className="rewind__menu-title">{t("rewind.anchor")}</div>
              {rewindDisabled && <div className="rewind__menu-hint">{t("rewind.disabledRunning")}</div>}
              <button
                className={confirmScope === "both" ? "rewind__menu-danger" : ""}
                type="button"
                disabled={rewindDisabled}
                onClick={() => selectRewind("both")}
              >
                {confirmScope === "both" ? t("rewind.confirmBoth") : t("rewind.both")}
              </button>
              <button
                className={confirmScope === "conversation" ? "rewind__menu-danger" : ""}
                type="button"
                disabled={rewindDisabled}
                onClick={() => selectRewind("conversation")}
              >
                {confirmScope === "conversation" ? t("rewind.confirmConversation") : t("rewind.conversation")}
              </button>
              <button
                className={confirmScope === "code" ? "rewind__menu-danger" : ""}
                type="button"
                disabled={rewindDisabled}
                onClick={() => selectRewind("code")}
              >
                {confirmScope === "code" ? t("rewind.confirmCode") : t("rewind.code")}
              </button>
              <button type="button" disabled={rewindDisabled} onClick={() => selectRewind("fork")}>{t("rewind.fork")}</button>
              <div className="rewind__menu-separator" />
              <button type="button" disabled={rewindDisabled} onClick={() => selectRewind("summ-from")}>{t("rewind.summFrom")}</button>
              <button type="button" disabled={rewindDisabled} onClick={() => selectRewind("summ-upto")}>{t("rewind.summUpto")}</button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// memo: an unchanged message keeps a stable `item` ref across a streaming turn's
// per-token re-renders, so only the live bubble re-parses markdown, not the whole
// backlog.
export const AssistantMessage = memo(function AssistantMessage({ item }: { item: AssistantItem }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  return (
    <div className="msg msg--assistant">
      {item.reasoning && (
        <div className="reasoning">
          <button className="reasoning__toggle" onClick={() => setOpen((v) => !v)}>
            <ChevronRight
              className={`reasoning__chevron ${open ? "reasoning__chevron--open" : ""}`}
              size={12}
            />
            {t("msg.thinking")}
          </button>
          {open && <div className="reasoning__body">{item.reasoning}</div>}
        </div>
      )}
      <div className="msg__body">
        {item.streaming ? (
          // While streaming, render raw text (stable, monospace-free) instead of
          // re-parsing markdown on every token — partial markdown reflows the
          // layout and makes the view jitter. Markdown renders once, on completion.
          <div className="msg__stream">
            {item.text}
            <span className="cursor" />
          </div>
        ) : (
          <Markdown text={item.text} />
        )}
      </div>
      {!item.streaming && item.text && (
        <div className="msg__actions">
          <CopyButton text={item.text} label={t("msg.copy")} />
        </div>
      )}
    </div>
  );
});
