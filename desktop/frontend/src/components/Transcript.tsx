import { type CSSProperties, useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { Item, LiveStream } from "../lib/useController";
import { useT } from "../lib/i18n";
import { AssistantMessage, UserMessage } from "./Message";
import { ToolCard } from "./ToolCard";
import { Welcome } from "./Welcome";

type ToolItem = Extract<Item, { kind: "tool" }>;
type QuestionAnchor = { id: string; text: string; turn: number };

const QUESTION_NAV_MIN_COUNT = 4;

function questionAnchorId(id: string): string {
  return `question-anchor-${id}`;
}

function compactQuestionText(text: string): string {
  const cleaned = text.replace(/@\.reasonix\/attachments\/[^\s]+/g, "[image]").replace(/\s+/g, " ").trim();
  if (cleaned.length <= 58) return cleaned;
  return `${cleaned.slice(0, 57)}…`;
}

function scrollVersion(items: Item[]): string {
  return items
    .map((it) => {
      switch (it.kind) {
        case "assistant":
          return `${it.id}:a:${it.text.length}:${it.reasoning.length}:${it.streaming ? 1 : 0}`;
        case "tool":
          return `${it.id}:t:${it.name}:${it.status}:${it.args.length}:${it.output?.length ?? 0}:${it.error?.length ?? 0}:${it.truncated ? 1 : 0}`;
        default:
          return `${it.id}:${it.kind}`;
      }
    })
    .join("|");
}

function repinIfWasPinned(
  el: HTMLDivElement,
  stick: { current: boolean },
  frame: { current: number | null },
  containerHeightDelta: number,
) {
  const bottomDistance = el.scrollHeight - el.scrollTop - el.clientHeight;
  // + delta reconstructs the bottom distance from before the height changed
  if (!stick.current && bottomDistance + containerHeightDelta >= 80) return;
  stick.current = true;
  if (frame.current !== null) cancelAnimationFrame(frame.current);
  frame.current = requestAnimationFrame(() => {
    if (stick.current) el.scrollTop = el.scrollHeight;
    frame.current = null;
  });
}

export function Transcript({
  items,
  live,
  footerHeight = 0,
  onPrompt,
  onRewind,
  questionNavigator = true,
}: {
  items: Item[];
  live?: LiveStream;
  footerHeight?: number;
  onPrompt: (text: string) => void;
  onRewind?: (turn: number, scope: string) => void;
  questionNavigator?: boolean;
}) {
  const scrollRef = useRef<HTMLDivElement>(null);
  // stick tracks whether the view is pinned to the bottom; once the user scrolls
  // up to read, we stop yanking them back down.
  const stick = useRef(true);
  const resizeFrame = useRef<number | null>(null);
  const questionNavFrame = useRef<number | null>(null);
  const lastClientHeight = useRef<number | null>(null);
  const lastFooterHeight = useRef<number | null>(null);

  const questions = useMemo<QuestionAnchor[]>(() => {
    const anchors: QuestionAnchor[] = [];
    let turn = 0;
    for (const it of items) {
      if (it.kind !== "user") continue;
      anchors.push({ id: it.id, text: compactQuestionText(it.text), turn });
      turn += 1;
    }
    return anchors;
  }, [items]);
  const showQuestionNav = questionNavigator && questions.length >= QUESTION_NAV_MIN_COUNT;
  const [activeQuestionId, setActiveQuestionId] = useState<string | null>(null);

  const updateQuestionNav = useCallback(() => {
    const el = scrollRef.current;
    if (!el || !showQuestionNav) {
      setActiveQuestionId((cur) => (cur === null ? cur : null));
      return;
    }
    const activeLine = el.scrollTop + Math.min(el.clientHeight * 0.3, 220);
    let nextActiveId = questions[0]?.id ?? null;
    for (const question of questions) {
      const node = document.getElementById(questionAnchorId(question.id));
      if (!node) continue;
      if (node.offsetTop <= activeLine) nextActiveId = question.id;
    }
    setActiveQuestionId((cur) => (cur === nextActiveId ? cur : nextActiveId));
  }, [questions, showQuestionNav]);

  const requestQuestionNavUpdate = useCallback(() => {
    if (questionNavFrame.current !== null) return;
    questionNavFrame.current = requestAnimationFrame(() => {
      questionNavFrame.current = null;
      updateQuestionNav();
    });
  }, [updateQuestionNav]);

  const onScroll = () => {
    const el = scrollRef.current;
    if (el) stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
    requestQuestionNavUpdate();
  };

  // Follow new content by setting scrollTop directly (no scrollIntoView fighting
  // the browser's scroll anchoring), and inside rAF so layout has settled first —
  // together with plain-text streaming this keeps the view from jittering. The
  // dependency tracks rendered content, not just array identity, so streaming
  // still follows the bottom if a reducer reuses the items array.
  // scrollVersion is O(items); recompute only when the backlog changes, not on
  // every streamed token. The live bubble's growth drives follow-to-bottom via
  // its length added to the effect deps below.
  const contentVersion = useMemo(() => scrollVersion(items), [items]);
  useEffect(() => {
    if (!stick.current) return;
    const el = scrollRef.current;
    if (!el) return;
    const id = requestAnimationFrame(() => {
      el.scrollTop = el.scrollHeight;
    });
    return () => cancelAnimationFrame(id);
  }, [contentVersion, live?.text.length, live?.reasoning.length]);

  useEffect(() => {
    requestQuestionNavUpdate();
  }, [contentVersion, footerHeight, live?.text.length, live?.reasoning.length, requestQuestionNavUpdate]);

  useEffect(() => {
    const el = scrollRef.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    lastClientHeight.current = el.clientHeight;
    const observer = new ResizeObserver(() => {
      const previous = lastClientHeight.current ?? el.clientHeight;
      lastClientHeight.current = el.clientHeight;
      repinIfWasPinned(el, stick, resizeFrame, el.clientHeight - previous);
      requestQuestionNavUpdate();
    });
    observer.observe(el);
    return () => {
      observer.disconnect();
      if (resizeFrame.current !== null) {
        cancelAnimationFrame(resizeFrame.current);
        resizeFrame.current = null;
      }
    };
  }, [requestQuestionNavUpdate]);

  useEffect(
    () => () => {
      if (questionNavFrame.current !== null) {
        cancelAnimationFrame(questionNavFrame.current);
        questionNavFrame.current = null;
      }
    },
    [],
  );

  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const previous = lastFooterHeight.current ?? footerHeight;
    lastFooterHeight.current = footerHeight;
    repinIfWasPinned(el, stick, resizeFrame, previous - footerHeight);
    return () => {
      if (resizeFrame.current !== null) {
        cancelAnimationFrame(resizeFrame.current);
        resizeFrame.current = null;
      }
    };
  }, [footerHeight]);

  // Sub-agent calls carry a parentId; collect them under their parent `task`
  // call so the parent card can render them nested, and skip them at top level.
  // Memoized so a `task` card's `subcalls` ref stays stable and its memo holds
  // across a streaming turn's per-token re-renders.
  const subcallsByParent = useMemo(() => {
    const m = new Map<string, ToolItem[]>();
    for (const it of items) {
      if (it.kind === "tool" && it.parentId) {
        const arr = m.get(it.parentId) ?? [];
        arr.push(it);
        m.set(it.parentId, arr);
      }
    }
    return m;
  }, [items]);

  // The rewind menu's open state is lifted here so at most one is open at a time;
  // a mousedown outside any .rewind closes it.
  const [openTurn, setOpenTurn] = useState<number | null>(null);
  useEffect(() => {
    if (openTurn === null) return;
    const onDown = (e: MouseEvent) => {
      const el = e.target as Element | null;
      if (!el || !el.closest(".rewind")) setOpenTurn(null);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [openTurn]);

  // Each user message's turn = its ordinal among user messages, so a rewind
  // targets the matching checkpoint.
  const userTurn = useMemo(() => new Map(questions.map((question) => [question.id, question.turn])), [questions]);

  const empty = items.length === 0;

  return (
    <div
      className={`transcript${empty ? " transcript--empty" : ""}${showQuestionNav ? " transcript--with-question-nav" : ""}`}
      ref={scrollRef}
      onScroll={onScroll}
    >
      {empty && <Welcome onPrompt={onPrompt} />}

      {!empty && showQuestionNav && (
        <QuestionNavigator questions={questions} activeId={activeQuestionId} />
      )}

      {items.map((it) => {
        switch (it.kind) {
          case "user": {
            const tn = userTurn.get(it.id);
            return (
              <UserMessage
                key={it.id}
                text={it.text}
                turn={tn}
                anchorId={questionAnchorId(it.id)}
                open={tn != null && openTurn === tn}
                onToggle={() => setOpenTurn((cur) => (cur === tn ? null : (tn ?? null)))}
                onRewind={(turn, scope) => {
                  onRewind?.(turn, scope);
                  setOpenTurn(null);
                }}
              />
            );
          }
          case "assistant": {
            // The streaming segment's text lives in `live`, not in items, so the
            // backlog ref stays stable per token; overlay it only on its own item.
            const shown = live && live.id === it.id ? { ...it, text: live.text, reasoning: live.reasoning, streaming: true } : it;
            return <AssistantMessage key={it.id} item={shown} />;
          }
          case "tool":
            if (it.parentId) return null; // rendered nested under its parent
            if (it.name === "todo_write") return null; // shown live in the pinned TodoPanel
            if (it.name === "exit_plan_mode") return null; // the plan was shown in the approval card
            return <ToolCard key={it.id} item={it} subcalls={subcallsByParent.get(it.id)} />;
          case "phase":
            return (
              <div key={it.id} className="phase">
                {it.text}
              </div>
            );
          case "notice":
            return (
              <div key={it.id} className={`notice notice--${it.level}`}>
                {it.text}
              </div>
            );
          case "compaction":
            return <CompactionCard key={it.id} item={it} />;
        }
      })}
    </div>
  );
}

function QuestionNavigator({
  questions,
  activeId,
}: {
  questions: QuestionAnchor[];
  activeId: string | null;
}) {
  const t = useT();
  const fallbackStep = questions.length > 1 ? 90 / (questions.length - 1) : 0;
  const jumpToQuestion = (id: string) => {
    const node = document.getElementById(questionAnchorId(id));
    node?.scrollIntoView({ behavior: "smooth", block: "start" });
  };
  return (
    <nav className="question-nav" aria-label={t("questionNav.label")}>
      <div className="question-nav__track">
        {questions.map((question, index) => {
          const top = 5 + index * fallbackStep;
          const active = question.id === activeId;
          return (
            <button
              key={question.id}
              type="button"
              className={`question-nav__mark${active ? " question-nav__mark--active" : ""}`}
              style={{ "--question-nav-top": `${top}%` } as CSSProperties}
              aria-label={t("questionNav.jump", { n: question.turn + 1 })}
              onClick={() => jumpToQuestion(question.id)}
            >
              <span className="question-nav__line" />
              <span className="question-nav__tip" role="tooltip">
                <span className="question-nav__tip-kicker">
                  {t("questionNav.progress", { current: question.turn + 1, total: questions.length })}
                </span>
                <span className="question-nav__tip-title">{question.text}</span>
                <span className="question-nav__tip-meta">{t("questionNav.hint")}</span>
              </span>
            </button>
          );
        })}
      </div>
    </nav>
  );
}

type CompactionItem = Extract<Item, { kind: "compaction" }>;

// CompactionCard marks a context-compaction boundary in the transcript. While
// the pass runs it shows a "compacting…" placeholder; once done it shows the
// message count and trigger with the summary collapsed behind a toggle (the
// summary is the new context base, so it's available but doesn't flood the view).
function CompactionCard({ item }: { item: CompactionItem }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  if (item.pending) {
    return (
      <div className="compaction compaction--pending">
        <span className="compaction__spinner">⋯</span> {t("compaction.working")}
      </div>
    );
  }
  return (
    <div className="compaction">
      <button className="compaction__head" onClick={() => setOpen((v) => !v)}>
        <span className="compaction__icon">◆</span>
        <span className="compaction__title">{t("compaction.title")}</span>
        <span className="compaction__meta">
          {t("compaction.messages", { n: item.messages })} · {item.trigger}
        </span>
        <span className="compaction__toggle">{open ? t("compaction.hideSummary") : t("compaction.showSummary")}</span>
      </button>
      {open && <pre className="compaction__summary">{item.summary}</pre>}
    </div>
  );
}
