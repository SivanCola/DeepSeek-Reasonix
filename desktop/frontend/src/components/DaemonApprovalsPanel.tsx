import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import type { DaemonApprovalDeskItemView, DaemonApprovalQuestionView, QuestionAnswer } from "../lib/types";
import { playAttentionChime } from "../lib/sound";
import { PromptAction, PromptBadge, PromptDetailToggle, PromptShelf } from "./PromptShelf";

const DAEMON_ADDR = "";
const REFRESH_MS = 8000;

function itemKey(item: DaemonApprovalDeskItemView): string {
  return `${item.sessionId}:${item.kind}:${item.id ?? ""}`;
}

function shortSessionID(sessionID: string): string {
  if (sessionID.length <= 12) return sessionID;
  return `${sessionID.slice(0, 6)}…${sessionID.slice(-4)}`;
}

function itemSummary(item: DaemonApprovalDeskItemView): string {
  if (item.kind === "approval") {
    return [item.tool, item.subject].filter(Boolean).join(" · ");
  }
  const firstQuestion = item.questions?.find((q) => q.prompt)?.prompt;
  return item.subject || firstQuestion || item.reason || "";
}

function answered(question: DaemonApprovalQuestionView, selected: Record<string, string[]>, custom: Record<string, string>): boolean {
  const id = question.id ?? "";
  return (selected[id]?.length ?? 0) > 0 || custom[id]?.trim() !== "";
}

function answerPayload(questions: DaemonApprovalQuestionView[], selected: Record<string, string[]>, custom: Record<string, string>): QuestionAnswer[] {
  return questions.map((question, index) => {
    const id = question.id || `q${index + 1}`;
    const typed = custom[id]?.trim();
    return { questionId: id, selected: typed ? [typed] : (selected[id] ?? []) };
  });
}

export function DaemonApprovalsPanel() {
  const t = useT();
  const [items, setItems] = useState<DaemonApprovalDeskItemView[]>([]);
  const [index, setIndex] = useState(0);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [selected, setSelected] = useState<Record<string, string[]>>({});
  const [custom, setCustom] = useState<Record<string, string>>({});
  const [working, setWorking] = useState(false);
  const [error, setError] = useState("");
  const previousFirstKeyRef = useRef("");
  const shelfRef = useRef<HTMLDivElement | null>(null);

  const refresh = useCallback(async () => {
    try {
      const next = await app.ListDaemonApprovals(DAEMON_ADDR);
      setItems(next);
      setError("");
      setIndex((current) => Math.min(current, Math.max(0, next.length - 1)));
      const firstKey = next[0] ? itemKey(next[0]) : "";
      if (firstKey && firstKey !== previousFirstKeyRef.current) {
        playAttentionChime();
      }
      previousFirstKeyRef.current = firstKey;
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setItems([]);
    }
  }, []);

  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => void refresh(), REFRESH_MS);
    return () => window.clearInterval(timer);
  }, [refresh]);

  const item = items[index];
  const key = item ? itemKey(item) : "";
  const questions = item?.questions ?? [];

  useEffect(() => {
    shelfRef.current?.focus();
    setDetailsOpen(false);
    setSelected({});
    setCustom({});
  }, [key]);

  const meta = useMemo(() => {
    if (!item) return "";
    const summary = itemSummary(item);
    const session = shortSessionID(item.sessionId);
    return [session, item.goalText, summary].filter(Boolean).join(" · ");
  }, [item]);

  if (!item) {
    return null;
  }

  const toggleOption = (question: DaemonApprovalQuestionView, label: string) => {
    const id = question.id ?? "";
    const cur = selected[id] ?? [];
    const next = question.multi
      ? (cur.includes(label) ? cur.filter((value) => value !== label) : [...cur, label])
      : [label];
    setSelected((current) => ({ ...current, [id]: next }));
    setCustom((current) => ({ ...current, [id]: "" }));
  };

  const setTyped = (question: DaemonApprovalQuestionView, value: string) => {
    const id = question.id ?? "";
    setCustom((current) => ({ ...current, [id]: value }));
    if (value.trim()) setSelected((current) => ({ ...current, [id]: [] }));
  };

  const handleApproval = async (allow: boolean, session: boolean, persist: boolean) => {
    if (working) return;
    if (!item.id) return;
    setWorking(true);
    try {
      await app.ApproveDaemon(item.sessionId, item.id, allow, session, persist, DAEMON_ADDR);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setWorking(false);
    }
  };

  const canSubmitAsk = questions.length === 0
    ? custom.__fallback?.trim() !== ""
    : questions.every((question) => answered(question, selected, custom));

  const handleAsk = async () => {
    if (working) return;
    if (!item.id || !canSubmitAsk) return;
    setWorking(true);
    try {
      await app.AnswerDaemonQuestion(
        item.sessionId,
        item.id,
        questions.length > 0 ? answerPayload(questions, selected, custom) : [],
        questions.length === 0 ? (custom.__fallback ?? "").trim() : "",
        DAEMON_ADDR,
      );
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setWorking(false);
    }
  };

  const badges = (
    <>
      <PromptBadge>{item.kind === "ask" ? t("daemonApprovals.askBadge") : t("daemonApprovals.approvalBadge")}</PromptBadge>
      <PromptBadge>{t("daemonApprovals.count", { current: index + 1, total: items.length })}</PromptBadge>
      {!item.active && <PromptBadge>{t("daemonApprovals.dormant")}</PromptBadge>}
    </>
  );

  return (
    <PromptShelf
      barRef={shelfRef}
      titleId="daemon-approvals-title"
      title={t("daemonApprovals.title")}
      badges={badges}
      meta={meta}
      actionsWrap
      actions={
        <>
          {items.length > 1 && (
            <button
              className="prompt-action prompt-action--quiet"
              disabled={working}
              onClick={() => setIndex((current) => (current + 1) % items.length)}
            >
              <span className="prompt-action__label">{t("daemonApprovals.next")}</span>
            </button>
          )}
          <PromptDetailToggle
            open={detailsOpen}
            label={t("approval.details")}
            openLabel={t("approval.hideDetails")}
            onClick={() => setDetailsOpen((open) => !open)}
          />
          {item.kind === "approval" ? (
            <>
              <PromptAction keyLabel="1" label={t("approval.allowOnce")} onClick={() => void handleApproval(true, false, false)} selected />
              <PromptAction keyLabel="2" label={t("approval.allowRuleSession")} onClick={() => void handleApproval(true, true, false)} />
              <PromptAction keyLabel="3" label={t("approval.allowRulePersistent")} onClick={() => void handleApproval(true, true, true)} />
              <PromptAction keyLabel="4" label={t("approval.deny")} onClick={() => void handleApproval(false, false, false)} />
            </>
          ) : (
            <button className="prompt-action prompt-action--selected" disabled={working || !canSubmitAsk} onClick={() => void handleAsk()}>
              <span className="prompt-action__label">{t("daemonApprovals.submitAnswer")}</span>
            </button>
          )}
        </>
      }
    >
      {(detailsOpen || item.kind === "ask" || error) && (
        <div className="daemon-approvals__panel">
          {error && <div className="daemon-approvals__error">{t("daemonApprovals.actionFailed", { err: error })}</div>}
          <div className="daemon-approvals__meta">
            <span>{t("daemonApprovals.session", { id: item.sessionId })}</span>
            {item.runStatus && <span>{item.runStatus}</span>}
          </div>
          {item.kind === "approval" && item.subject && <pre className="approval-subject">{item.subject}</pre>}
          {item.kind === "ask" && (
            <div className="daemon-approvals__questions">
              {questions.length > 0 ? questions.map((question, questionIndex) => {
                const qid = question.id || `q${questionIndex + 1}`;
                return (
                  <div className="daemon-approvals__question" key={qid}>
                    <div className="daemon-approvals__prompt">
                      <span>{question.header || t("ask.questionProgress", { progress: `${questionIndex + 1}/${questions.length}` })}</span>
                      {question.prompt && <b>{question.prompt}</b>}
                    </div>
                    {question.options && question.options.length > 0 && (
                      <div className="daemon-approvals__options">
                        {question.options.map((option) => {
                          const on = (selected[qid] ?? []).includes(option.label);
                          return (
                            <button
                              className={`daemon-approvals__option${on ? " daemon-approvals__option--selected" : ""}`}
                              key={option.label}
                              type="button"
                              onClick={() => toggleOption(question, option.label)}
                            >
                              <span>{option.label}</span>
                              {option.description && <small>{option.description}</small>}
                            </button>
                          );
                        })}
                      </div>
                    )}
                    <input
                      className="ask-shelf__custom"
                      placeholder={t("ask.customPlaceholder")}
                      value={custom[qid] ?? ""}
                      onChange={(event) => setTyped(question, event.target.value)}
                    />
                  </div>
                );
              }) : (
                <input
                  className="ask-shelf__custom"
                  placeholder={t("daemonApprovals.answerPlaceholder")}
                  value={custom.__fallback ?? ""}
                  onChange={(event) => setCustom((current) => ({ ...current, __fallback: event.target.value }))}
                />
              )}
            </div>
          )}
        </div>
      )}
    </PromptShelf>
  );
}
