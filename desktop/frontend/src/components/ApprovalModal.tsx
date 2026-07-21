import { useCallback, useEffect, useRef, useState } from "react";
import type { KeyboardEvent as ReactKeyboardEvent } from "react";
import gsap from "gsap";
import { useT, type Translator } from "../lib/i18n";
import type { ComposerInsertRequest, DirEntry, ToolApprovalMode, WireApproval } from "../lib/types";
import {
  DecisionConfirmBar,
  PromptAction,
  PromptBadge,
  PromptHeaderAction,
  PromptShelf,
} from "./PromptShelf";
import { DUR_FAST } from "../lib/gsapAnimations";
import {
  FileReferenceMenu,
  insertTextAtSelection,
  pickInlineFileReference,
  useFileReferenceMenu,
} from "./FileReferenceMenu";

function animateShelfExit(
  el: HTMLDivElement,
  options: { opacity: number; y: number; duration: number; ease: string; onComplete: () => void },
) {
  const animator = typeof gsap.to === "function"
    ? gsap
    : (gsap as unknown as { default?: typeof gsap }).default;
  if (animator && typeof animator.to === "function") {
    animator.to(el, options);
    return;
  }
  options.onComplete();
}

function requiresFreshHumanApproval(tool: string): boolean {
  return tool === "remember" || tool === "forget" || tool === "exit_plan_mode" || tool === "sandbox_escape" || tool === "config_write";
}

const APPROVAL_MODE_RANK: Record<ToolApprovalMode, number> = { ask: 0, auto: 1, yolo: 2 };

export function approvalToolLabel(tool: string, t: Translator): string {
  switch (tool) {
    case "bash":
      return t("approval.toolLabelBash");
    case "edit_file":
      return t("approval.toolLabelEditFile");
    case "write_file":
      return t("approval.toolLabelWriteFile");
    case "multi_edit":
      return t("approval.toolLabelMultiEdit");
    case "move_file":
      return t("approval.toolLabelMoveFile");
    case "web_fetch":
      return t("approval.toolLabelWebFetch");
    case "run_skill":
      return t("approval.toolLabelRunSkill");
    case "remember":
      return t("approval.toolLabelRemember");
    case "forget":
      return t("approval.toolLabelForget");
    case "sandbox_escape":
      return t("approval.toolLabelSandboxEscape");
    case "config_write":
      return t("approval.toolLabelConfigWrite");
    case "plan_mode_read_only_command":
      return t("approval.toolLabelPlanModeReadOnly");
    case "exit_plan_mode":
      return t("approval.toolLabelExitPlan");
    default:
      return tool;
  }
}

const sandboxEscapeEnglishSubjectFallback = "run shell command unconfined once";
const sandboxEscapeEnglishSubjectPrefix = "run unconfined once: ";
const configWriteEnglishSubjectPrefix = "write Reasonix config: ";
const planModeBashEnglishSubject = /^Trust (.+) as a read-only command prefix while planning\r?\nCommand: ([\s\S]+)$/;

function localizeApprovalSubject(tool: string, subject: string, t: Translator): string {
  const trimmed = subject.trim();
  if (tool === "sandbox_escape") {
    if (!trimmed || trimmed === sandboxEscapeEnglishSubjectFallback) return t("approval.sandboxEscapeSubjectFallback");
    if (trimmed.startsWith(sandboxEscapeEnglishSubjectPrefix)) {
      return `${t("approval.sandboxEscapeSubjectPrefix")}${trimmed.slice(sandboxEscapeEnglishSubjectPrefix.length)}`;
    }
    return trimmed;
  }
  if (tool === "config_write") {
    if (trimmed.startsWith(configWriteEnglishSubjectPrefix)) {
      return `${t("approval.configWriteSubjectPrefix")}${trimmed.slice(configWriteEnglishSubjectPrefix.length)}`;
    }
    return trimmed;
  }
  if (tool === "remember") {
    return trimmed
      .replace(/^Save\/update memory/, t("approval.memorySaveUpdate"))
      .replace(/\bbody: /g, `${t("approval.memoryBodyLabel")}: `);
  }
  if (tool === "forget" && trimmed.startsWith("Archive memory ")) {
    return `${t("approval.memoryArchivePrefix")}${trimmed.slice("Archive memory ".length)}`;
  }
  const bashTrust = trimmed.match(planModeBashEnglishSubject);
  if (bashTrust) {
    return t("approval.planModeBashTrustSubject", { prefix: bashTrust[1] ?? "", command: bashTrust[2] ?? "" });
  }
  return trimmed;
}

function localizeApprovalReason(tool: string, reason: string | undefined, t: Translator): string {
  const trimmed = reason?.trim() ?? "";
  if (tool === "config_write") {
    if (!trimmed || trimmed.includes("Reasonix-managed configuration file")) return t("approval.configWriteReason");
    return trimmed;
  }
  if (tool !== "sandbox_escape") return trimmed;
  if (trimmed.includes("could not wrap this command") || trimmed.includes("does not provide an OS-level Bash sandbox")) {
    return t("approval.sandboxEscapeWrapReason");
  }
  if (
    trimmed.includes("failed while starting this command") ||
    trimmed.includes("could not start this command") ||
    trimmed.includes("Run this command unconfined once?")
  ) {
    return t("approval.sandboxEscapeRuntimeReason");
  }
  return trimmed || t("approval.sandboxEscapeRuntimeReason");
}

function localizePlanModeApprovalReason(tool: string, reason: string, t: Translator): string {
  if (tool === "plan_mode_read_only_command" && reason.includes("built-in read-only set")) {
    return t("approval.planModeBashTrustReason");
  }
  return reason;
}

type DecisionAction = {
  key: string;
  label: string;
  desc: string;
  tone?: "default" | "danger";
  // Plan revision toggles the inline editor instead of submitting.
  // Recovery uses direct-click submit (no select-then-confirm).
  kind: "submit" | "toggle-revision" | "direct";
  run?: () => void;
};

const RECOVERY_FEEDBACK_MAX = 1000;

function recoveryReasonText(
  changeKind: string | undefined,
  fallback: string | undefined,
  t: Translator,
): string {
  switch ((changeKind ?? "").toLowerCase()) {
    case "risk":
      return t("approval.recoveryReasonRisk");
    case "scope":
      return t("approval.recoveryReasonScope");
    case "strategy":
      return t("approval.recoveryReasonStrategy");
    case "uncertain":
    case "same_strategy":
      return t("approval.recoveryReasonUncertain");
    default:
      return fallback?.trim() || t("approval.recoveryReasonUncertain");
  }
}

export function ApprovalModal({
  approval,
  onAnswer,
  onResolveRecovery,
  onRevisePlan,
  onExitPlan,
  onStop,
  cwd,
  tabId,
  workspaceScopeKey,
  insertRequest,
  onRevisionActiveChange,
  toolApprovalMode,
}: {
  approval: WireApproval;
  onAnswer: (allow: boolean, session: boolean, persist: boolean) => void;
  onResolveRecovery?: (action: "continue" | "revise", feedback?: string) => void;
  onRevisePlan?: (text: string) => void;
  onExitPlan?: () => void;
  onStop: () => void;
  cwd?: string;
  tabId?: string;
  workspaceScopeKey?: string;
  insertRequest?: ComposerInsertRequest | null;
  onRevisionActiveChange?: (active: boolean) => void;
  toolApprovalMode?: ToolApprovalMode;
}) {
  const t = useT();
  const isPlanApproval = approval.tool === "exit_plan_mode";
  const isRecoveryApproval = approval.kind === "recovery" || Boolean(approval.recovery);
  const toolLabel = isRecoveryApproval ? t("approval.toolLabelRecovery") : approvalToolLabel(approval.tool, t);
  const isFreshHumanApproval = approval.fresh === true || requiresFreshHumanApproval(approval.tool) || isRecoveryApproval;
  const hasFreshSessionGrant = approval.tool === "sandbox_escape" || approval.tool === "config_write";
  // Switching the approval segmented control to a more permissive mode does not
  // resolve an already-pending request; say so on the card instead of leaving
  // the user to wonder why the switch "did nothing".
  const initialToolApprovalModeRef = useRef(toolApprovalMode);
  const approvalModeRelaxed =
    !isPlanApproval &&
    toolApprovalMode !== undefined &&
    initialToolApprovalModeRef.current !== undefined &&
    APPROVAL_MODE_RANK[toolApprovalMode] > APPROVAL_MODE_RANK[initialToolApprovalModeRef.current];
  const subject = localizeApprovalSubject(approval.tool, approval.subject, t);
  const reason = localizePlanModeApprovalReason(approval.tool, localizeApprovalReason(approval.tool, approval.reason, t), t);
  const subjectSummary = subject.split(/\r?\n/).find((line) => line.trim())?.trim() ?? "";
  // Plan approvals already show the plan above; keep a short hint. Tool
  // approvals surface the command/subject by default (reason is secondary).
  const toolMeta = isPlanApproval ? t("approval.planReadyHint") : (subjectSummary || reason || approval.tool);
  const hasToolDetails = Boolean(reason || subject);
  // Subject (command) is visible by default; long reason can collapse.
  const [reasonOpen, setReasonOpen] = useState(() => {
    if (isRecoveryApproval) return false; // recovery details stay collapsed
    return Boolean(reason) && reason.length <= 160;
  });
  // Default: allow once (tool) or start execution (plan index 1).
  // Recovery has no default selection so Enter cannot accidentally continue.
  const [selectedIndex, setSelectedIndex] = useState(() => (isPlanApproval ? 1 : isRecoveryApproval ? -1 : 0));
  const [revisionOpen, setRevisionOpen] = useState(false);
  const [revisionText, setRevisionText] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const cardRef = useRef<HTMLDivElement | null>(null);
  const shelfRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  const consumedInsertIdRef = useRef(0);
  // When consecutive approvals arrive, animate the old card out before
  // the new one slides in.  GSAP fromTo on the shelf wrapper avoids the
  // jarring pop when the API cycles through 4+ pending approvals.
  const closingRef = useRef(false);
  const fileMenu = useFileReferenceMenu(revisionText, cwd, tabId, workspaceScopeKey);

  const answerWithExit = (fn: () => void) => {
    if (closingRef.current || submitting) return;
    closingRef.current = true;
    setSubmitting(true);
    const el = shelfRef.current;
    if (el) {
      animateShelfExit(el, {
        opacity: 0,
        y: 8,
        duration: DUR_FAST,
        ease: "power2.in",
        onComplete: fn,
      });
    } else {
      fn();
    }
  };

  const resolveRecovery = useCallback(
    (action: "continue" | "revise", feedback?: string) => {
      const resolve = onResolveRecovery ?? ((a: "continue" | "revise") => onAnswer(a === "continue", false, false));
      if (action === "revise") {
        const text = (feedback ?? revisionText).trim().slice(0, RECOVERY_FEEDBACK_MAX);
        resolve("revise", text || undefined);
        return;
      }
      resolve("continue");
    },
    [onResolveRecovery, onAnswer, revisionText],
  );

  const toolActions: DecisionAction[] = isRecoveryApproval
    ? [
        {
          key: "1",
          label: t("approval.recoveryContinue"),
          desc: t("approval.recoveryContinueDesc"),
          kind: "direct",
          run: () => resolveRecovery("continue"),
        },
        {
          key: "2",
          label: t("approval.recoveryRevise"),
          desc: t("approval.recoveryReviseDesc"),
          kind: "direct",
          run: () => resolveRecovery("revise"),
        },
      ]
    : isPlanApproval
    ? [
        {
          key: "1",
          label: t("approval.revisePlan"),
          desc: t("approval.revisePlanDesc"),
          kind: "toggle-revision",
        },
        {
          key: "2",
          label: t("approval.startExecution"),
          desc: t("approval.startExecutionDesc"),
          kind: "submit",
          run: () => onAnswer(true, false, false),
        },
        {
          key: "3",
          label: t("approval.exitPlan"),
          desc: t("approval.exitPlanDesc"),
          tone: "danger",
          kind: "submit",
          run: () => (onExitPlan ?? (() => onAnswer(false, false, false)))(),
        },
      ]
    : [
        {
          key: "1",
          label: t("approval.allowOnce"),
          desc: t("approval.allowOnceDesc"),
          kind: "submit",
          run: () => onAnswer(true, false, false),
        },
        ...(isFreshHumanApproval
          ? hasFreshSessionGrant
            ? [
                {
                  key: "2",
                  label: t(approval.tool === "config_write" ? "approval.allowConfigWriteSession" : "approval.allowSandboxEscapeSession"),
                  desc: t(approval.tool === "config_write" ? "approval.allowConfigWriteSessionDesc" : "approval.allowSandboxEscapeSessionDesc"),
                  kind: "submit" as const,
                  run: () => onAnswer(true, true, false),
                },
                {
                  key: "3",
                  label: t("approval.deny"),
                  desc: t("approval.denyDesc"),
                  tone: "danger" as const,
                  kind: "submit" as const,
                  run: () => onAnswer(false, false, false),
                },
              ]
            : [
                {
                  key: "2",
                  label: t("approval.deny"),
                  desc: t("approval.denyDesc"),
                  tone: "danger" as const,
                  kind: "submit" as const,
                  run: () => onAnswer(false, false, false),
                },
              ]
          : [
              {
                key: "2",
                label: t("approval.allowRuleSession"),
                desc: t("approval.allowRuleSessionDesc"),
                kind: "submit" as const,
                run: () => onAnswer(true, true, false),
              },
              {
                key: "3",
                label: t("approval.allowRulePersistent"),
                desc: t("approval.allowRulePersistentDesc"),
                kind: "submit" as const,
                run: () => onAnswer(true, true, true),
              },
              {
                key: "4",
                label: t("approval.deny"),
                desc: t("approval.denyDesc"),
                tone: "danger" as const,
                kind: "submit" as const,
                run: () => onAnswer(false, false, false),
              },
            ]),
      ];

  const actionCount = toolActions.length;
  const selectedIndexRef = useRef(selectedIndex);
  selectedIndexRef.current = selectedIndex;
  const selectedAction = toolActions[Math.min(selectedIndex, actionCount - 1)] ?? toolActions[0];

  useEffect(() => {
    cardRef.current?.focus();
    setRevisionOpen(false);
    setRevisionText("");
    setReasonOpen(isRecoveryApproval ? false : Boolean(reason) && reason.length <= 160);
    setSelectedIndex(isPlanApproval ? 1 : isRecoveryApproval ? -1 : 0);
    setSubmitting(false);
    closingRef.current = false;
  }, [approval.id, isPlanApproval, isRecoveryApproval, reason]);

  const confirmSelected = useCallback(() => {
    if (submitting || closingRef.current) return;
    if (isRecoveryApproval) {
      // Recovery never auto-submits on Enter without an explicit selection.
      const action = toolActions[selectedIndexRef.current];
      if (!action?.run) return;
      answerWithExit(action.run);
      return;
    }
    const action = toolActions[selectedIndexRef.current];
    if (!action) return;
    if (action.kind === "toggle-revision") {
      setRevisionOpen((open) => !open);
      return;
    }
    if (action.run) answerWithExit(action.run);
  }, [submitting, toolActions, isRecoveryApproval]);

  useEffect(() => {
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (submitting) return;
      const target = event.target instanceof Element ? event.target : null;
      const tag = target?.tagName.toLowerCase();
      // Editing revision / file menu owns arrows and digits while focused.
      // Optional recovery feedback also disables digit shortcuts.
      const editing =
        tag === "input" ||
        tag === "textarea" ||
        tag === "select" ||
        (target instanceof HTMLElement && target.isContentEditable) ||
        (isRecoveryApproval && revisionOpen);
      if (editing && (event.key === "1" || event.key === "2" || event.key === "3" || event.key === "4")) {
        return;
      }
      if (tag === "input" || tag === "textarea" || tag === "select" || (target instanceof HTMLElement && target.isContentEditable)) return;
      if (event.key === "ArrowUp") {
        event.preventDefault();
        setSelectedIndex((i) => {
          const base = i < 0 ? 0 : i;
          return (base - 1 + actionCount) % actionCount;
        });
      } else if (event.key === "ArrowDown") {
        event.preventDefault();
        setSelectedIndex((i) => {
          const base = i < 0 ? -1 : i;
          return (base + 1) % actionCount;
        });
      } else if (event.key === "Enter") {
        if (isRecoveryApproval && selectedIndexRef.current < 0) return;
        event.preventDefault();
        confirmSelected();
      } else if (event.key === "1" || event.key === "2" || event.key === "3" || event.key === "4") {
        if (isRecoveryApproval && revisionOpen) return;
        const index = Number(event.key) - 1;
        if (index < 0 || index >= actionCount) return;
        event.preventDefault();
        if (isRecoveryApproval) {
          // Digit keys on recovery cards submit directly (one-click).
          const action = toolActions[index];
          if (action?.run) answerWithExit(action.run);
          return;
        }
        setSelectedIndex(index);
      } else if (event.key === "Escape") {
        event.preventDefault();
        answerWithExit(onStop);
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [actionCount, confirmSelected, onStop, submitting, isRecoveryApproval, revisionOpen, toolActions]);

  useEffect(() => {
    if (revisionOpen) {
      onRevisionActiveChange?.(true);
      inputRef.current?.focus();
      return () => onRevisionActiveChange?.(false);
    }
    onRevisionActiveChange?.(false);
  }, [revisionOpen, onRevisionActiveChange]);

  const focusRevisionInput = (caret = revisionText.length) => {
    requestAnimationFrame(() => {
      const input = inputRef.current;
      if (!input) return;
      input.focus();
      input.setSelectionRange(caret, caret);
    });
  };

  const insertRevisionText = useCallback((text: string) => {
    const input = inputRef.current;
    const start = input?.selectionStart ?? revisionText.length;
    const end = input?.selectionEnd ?? start;
    const next = insertTextAtSelection(revisionText, text, start, end);
    setRevisionText(next.value);
    focusRevisionInput(next.caret);
  }, [revisionText]);

  useEffect(() => {
    if (!insertRequest || insertRequest.id === consumedInsertIdRef.current) return;
    consumedInsertIdRef.current = insertRequest.id;
    insertRevisionText(insertRequest.text);
  }, [insertRequest, insertRevisionText]);

  const pickRevisionFile = (entry: DirEntry) => {
    const next = pickInlineFileReference(revisionText, fileMenu.atRaw, fileMenu.atDir, entry);
    setRevisionText(next);
    focusRevisionInput(next.length);
  };

  const onRevisionKeyDown = (event: ReactKeyboardEvent<HTMLTextAreaElement>) => {
    if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
      submitRevision();
      event.stopPropagation();
      return;
    }
    if (fileMenu.open) {
      if (event.key === "ArrowDown" && fileMenu.count > 0) {
        event.preventDefault();
        fileMenu.setActive((index) => (index + 1) % fileMenu.count);
        return;
      }
      if (event.key === "ArrowUp" && fileMenu.count > 0) {
        event.preventDefault();
        fileMenu.setActive((index) => (index - 1 + fileMenu.count) % fileMenu.count);
        return;
      }
      if ((event.key === "Enter" || event.key === "Tab") && fileMenu.count > 0) {
        event.preventDefault();
        const entry = fileMenu.items[fileMenu.active];
        if (entry) pickRevisionFile(entry);
        return;
      }
      if (event.key === "Escape") {
        event.preventDefault();
        fileMenu.dismiss();
        return;
      }
    }
    event.stopPropagation();
  };

  const submitRevision = () => {
    if (isRecoveryApproval) {
      answerWithExit(() => resolveRecovery("revise", revisionText));
      return;
    }
    const text = revisionText.trim();
    if (!text) {
      inputRef.current?.focus();
      return;
    }
    answerWithExit(() => onRevisePlan?.(text));
  };

  const recovery = approval.recovery;
  const recoveryReason = isRecoveryApproval
    ? recoveryReasonText(
        recovery?.change_kind,
        recovery?.change_rationale || recovery?.review_rationale || reason,
        t,
      )
    : "";
  const recoveryActionSummary =
    recovery?.next_action ||
    recovery?.next_tool ||
    subjectSummary ||
    approval.tool;
  const recoveryMeta = isRecoveryApproval
    ? [recoveryReason, recoveryActionSummary && t("approval.recoveryNext", { action: recoveryActionSummary })]
        .filter(Boolean)
        .join(" · ")
    : "";

  const confirmIsDanger = selectedAction?.tone === "danger";
  const confirmLabel =
    selectedAction?.kind === "toggle-revision"
      ? revisionOpen
        ? t("common.cancel")
        : t("approval.revisePlan")
      : t("decision.confirm");

  return (
    <div ref={shelfRef}>
      <PromptShelf
        decision
        className={isPlanApproval ? "prompt-shelf--plan-approval" : isRecoveryApproval ? "prompt-shelf--recovery-approval" : "prompt-shelf--tool-approval"}
        barRef={cardRef}
        titleId={isPlanApproval ? "plan-approval-title" : isRecoveryApproval ? "recovery-approval-title" : "tool-approval-title"}
        title={isPlanApproval ? t("approval.planReady") : isRecoveryApproval ? t("approval.recoveryPending") : t("approval.toolPending")}
        badges={
          <>
            {!isPlanApproval && <PromptBadge tone="amber">{toolLabel}</PromptBadge>}
            {isPlanApproval && revisionOpen && <PromptBadge>{t("approval.revisePlan")}</PromptBadge>}
          </>
        }
        meta={isRecoveryApproval ? (recoveryMeta || toolMeta) : toolMeta}
        headerActions={
          <>
            {isRecoveryApproval && (
              <PromptHeaderAction onClick={() => setReasonOpen((open) => !open)} disabled={submitting}>
                {t(reasonOpen ? "approval.hideDetails" : "approval.details")}
              </PromptHeaderAction>
            )}
            {!isPlanApproval && !isRecoveryApproval && hasToolDetails && reason && (
              <PromptHeaderAction onClick={() => setReasonOpen((open) => !open)} disabled={submitting}>
                {t(reasonOpen ? "approval.hideDetails" : "approval.details")}
              </PromptHeaderAction>
            )}
            <PromptHeaderAction
              onClick={() => answerWithExit(onStop)}
              ariaLabel={t("decision.stopTask")}
              disabled={submitting}
            >
              {t("decision.stopTask")}
            </PromptHeaderAction>
          </>
        }
        actions={
          <>
            {toolActions.map((action, index) => (
              <PromptAction
                key={action.key}
                keyLabel={action.key}
                label={action.label}
                description={action.desc}
                onClick={() => {
                  if (submitting) return;
                  if (action.kind === "direct" && action.run) {
                    answerWithExit(action.run);
                    return;
                  }
                  setSelectedIndex(index);
                  if (action.kind === "toggle-revision") {
                    // Selecting revise opens the editor but still needs confirm
                    // only when the user hits Enter / Confirm (toggle on confirm).
                  }
                }}
                selected={selectedIndex === index}
                tone={action.tone}
                disabled={submitting}
                title={action.desc}
              />
            ))}
          </>
        }
        footer={
          isRecoveryApproval ? (
            <div className="recovery-optional-feedback">
              <button
                type="button"
                className="prompt-shelf__header-action"
                disabled={submitting}
                onClick={() => setRevisionOpen((open) => !open)}
              >
                {t(revisionOpen ? "approval.recoveryHideFeedback" : "approval.recoveryOptionalFeedback")}
              </button>
            </div>
          ) : (
            <DecisionConfirmBar
              hint={t("decision.selectHint")}
              confirmLabel={confirmLabel}
              onConfirm={confirmSelected}
              disabled={submitting}
              danger={confirmIsDanger}
            />
          )
        }
      >
        {(approvalModeRelaxed ||
          (isRecoveryApproval && (reasonOpen || revisionOpen)) ||
          (!isPlanApproval && !isRecoveryApproval && (subject || (reasonOpen && reason))) ||
          (isPlanApproval && revisionOpen)) && (
          <>
            {approvalModeRelaxed && !isRecoveryApproval && (
              <div className="approval-mode-hint">{t("approval.modeSwitchPendingHint")}</div>
            )}
            {isRecoveryApproval && reasonOpen && (
              <div className="approval-details recovery-details">
                {recoveryActionSummary && (
                  <div className="approval-reason">
                    <strong>{t("approval.recoveryNextLabel")}</strong>{" "}
                    {recovery?.next_tool ? `${recovery.next_tool}: ` : ""}
                    {recoveryActionSummary}
                  </div>
                )}
                {recovery?.failed_summary && (
                  <div className="approval-reason">
                    <strong>{t("approval.recoveryFailedLabel")}</strong>{" "}
                    {recovery.failed_tool ? `${recovery.failed_tool}: ` : ""}
                    {recovery.failed_summary}
                  </div>
                )}
                {recovery?.diagnosis && (
                  <div className="approval-reason">
                    <strong>{t("approval.recoveryDiagnosisLabel")}</strong> {recovery.diagnosis}
                  </div>
                )}
                {(recovery?.change_rationale || recovery?.review_rationale) && (
                  <div className="approval-reason">
                    <strong>{t("approval.recoveryWhyLabel")}</strong>{" "}
                    {recovery.change_rationale || recovery.review_rationale}
                  </div>
                )}
                {recovery?.source_agent && (
                  <div className="approval-reason">
                    <strong>{t("approval.recoverySourceLabel")}</strong> {recovery.source_agent}
                  </div>
                )}
                {!recovery?.failed_summary && !recovery?.diagnosis && subject && (
                  <pre className="approval-subject">{subject}</pre>
                )}
              </div>
            )}
            {!isPlanApproval && !isRecoveryApproval && subject && (
              <div className="approval-details">
                <pre className="approval-subject">{subject}</pre>
                {reasonOpen && reason && <div className="approval-reason">{reason}</div>}
              </div>
            )}
            {isRecoveryApproval && revisionOpen && (
              <div className="plan-revision">
                <textarea
                  ref={inputRef}
                  className="plan-revision__input"
                  value={revisionText}
                  rows={3}
                  maxLength={RECOVERY_FEEDBACK_MAX}
                  placeholder={t("approval.recoveryRevisePlaceholder")}
                  onChange={(event) => setRevisionText(event.target.value.slice(0, RECOVERY_FEEDBACK_MAX))}
                  onFocus={() => onRevisionActiveChange?.(true)}
                  onKeyDown={onRevisionKeyDown}
                  disabled={submitting}
                />
                {fileMenu.open && (
                  <FileReferenceMenu
                    items={fileMenu.items}
                    activeIndex={fileMenu.active}
                    onPick={pickRevisionFile}
                    onHover={fileMenu.setActive}
                  />
                )}
              </div>
            )}
            {isPlanApproval && revisionOpen && (
              <div className="plan-revision">
                <textarea
                  ref={inputRef}
                  className="plan-revision__input"
                  value={revisionText}
                  rows={3}
                  placeholder={t("approval.revisePlanPlaceholder")}
                  onChange={(event) => setRevisionText(event.target.value)}
                  onFocus={() => onRevisionActiveChange?.(true)}
                  onKeyDown={onRevisionKeyDown}
                  disabled={submitting}
                />
                {fileMenu.open && (
                  <FileReferenceMenu
                    items={fileMenu.items}
                    activeIndex={fileMenu.active}
                    onPick={pickRevisionFile}
                    onHover={fileMenu.setActive}
                  />
                )}
                <div className="plan-revision__actions">
                  <button className="btn" type="button" onClick={() => setRevisionOpen(false)} disabled={submitting}>
                    {t("common.cancel")}
                  </button>
                  <button className="btn btn--primary" type="button" onClick={submitRevision} disabled={submitting}>
                    {t("approval.sendRevision")}
                  </button>
                </div>
              </div>
            )}
          </>
        )}
      </PromptShelf>
    </div>
  );
}
