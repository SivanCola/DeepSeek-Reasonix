import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type CSSProperties,
} from "react";
import { ArrowDown, Loader2 } from "lucide-react";
import type { ControllerLiveStore, HistoryLoadTrigger, HistoryMutation, Item, LiveStream } from "../lib/useController";
import type { CheckpointMeta, WireCompletionSummary } from "../lib/types";
import type { InvocationMetadataMap } from "../lib/invocationDisplay";
import { useT } from "../lib/i18n";
import { acquireMarkdownWorkerClient, releaseMarkdownWorkerClient } from "../lib/markdownWorkerClient";
import { onSessionExperienceWillChange, useSessionExperience } from "../lib/sessionExperience";
import {
  buildTranscriptRowBlocks,
  buildTurnModels,
  EMPTY_FOLDS,
  foldMapWithReasoningOpen,
  foldMapWithToggle,
  foldSegmentStates,
  NO_LIVE,
  reconcileFoldEntries,
  type FoldMap,
  type ToolItem,
  type TranscriptLiveFlags,
} from "../lib/transcriptRows";
import { projectTranscriptTimeline, transcriptRenderMode } from "../lib/transcriptTimeline";
import {
  readTranscriptFoldOverrides,
  replaceTranscriptFoldOverrides,
  writeTranscriptFoldOverride,
} from "../lib/transcriptFoldOverrides";
import { useTranscriptKernel } from "../lib/useTranscriptKernel";
import { useTranscriptQuestions } from "../lib/useTranscriptQuestionNavigation";
import { useTranscriptSelectableRows } from "../lib/useTranscriptSelectableRows";
import { useTranscriptSelectionRetention } from "../lib/useTranscriptSelectionRetention";
import { useCreationTranscriptScrollbar } from "../lib/useCreationTranscriptScrollbar";
import { hasTranscriptScrollableRange } from "../lib/transcriptScrollGeometry";
import { attachNestedScrollHandoff } from "../lib/nestedScrollHandoff";
import { useTranscriptEntranceAnimation } from "../lib/useEntranceAnimation";
import type { QuestionAnchor } from "../lib/transcriptGrouping";
import { transcriptSelectionStore } from "../lib/transcriptSelectionStore";
import { recordFrontendDiagnostic } from "../lib/frontendDiagnosticBridge";
import { InvocationMetadataContext } from "./Message";
import { LiveStreamContext } from "./LiveStreamContext";
import { MarkdownImageTabContext } from "./MarkdownImageContext";
import { TranscriptLayoutIntentProvider, TranscriptScrollWriteProvider } from "./TranscriptLayoutIntentContext";
import { TranscriptViewport, type TranscriptViewportHandle } from "./TranscriptViewport";
import { Welcome } from "./Welcome";
import { useTranscriptRowRenderer } from "./useTranscriptRowRenderer";

export { NoticeCard } from "./TranscriptCards";

const EMPTY_CHECKPOINTS: CheckpointMeta[] = [];
const EMPTY_INVOCATION_METADATA: InvocationMetadataMap = {};
const QUESTION_NAV_MIN_COUNT = 2;
const QuestionJumpBar = lazy(() => import("./QuestionJumpBar"));
const SHOW_FRONTEND_DIAGNOSTICS = typeof __BUILD_CHANNEL__ === "undefined"
  || __BUILD_CHANNEL__ === "test"
  || __BUILD_CHANNEL__ === "preview"
  || __BUILD_CHANNEL__ === "canary"
  || Boolean(import.meta.env?.DEV);
const FrontendDiagnosticsPanel = SHOW_FRONTEND_DIAGNOSTICS
  ? lazy(() => import("./FrontendDiagnosticsPanel"))
  : null;

export type TranscriptProps = {
  items: Item[];
  live?: LiveStream;
  liveStore?: ControllerLiveStore;
  tabId?: string;
  geometrySessionKey?: string;
  footerHeight?: number;
  onPrompt: (text: string) => void;
  onDeliveryContinue?: () => void;
  onAcceptDelivery?: () => void;
  onOpenChanges?: () => void;
  onOpenVerification?: (summary: WireCompletionSummary) => void;
  onEditPrompt?: (turn: number, displayText: string, submitText?: string) => boolean | void | Promise<boolean | void>;
  onRewind?: (turn: number, scope: string) => void;
  checkpoints?: CheckpointMeta[];
  actionPending?: boolean;
  rewindDisabled?: boolean;
  running?: boolean;
  questionNavigator?: boolean;
  welcomeVariant?: "default" | "creation";
  creationMode?: boolean;
  actionHoverMenus?: boolean;
  rewindSignal?: number;
  revealSignal?: number;
  hydrating?: boolean;
  hasOlderHistory?: boolean;
  historyStartTurn?: number;
  historyTotalTurns?: number;
  loadingOlderHistory?: boolean;
  olderHistoryError?: string;
  onLoadOlderHistory?: (targetTurn?: number, trigger?: HistoryLoadTrigger) => boolean | Promise<boolean>;
  turnStartAt?: number;
  contentRevision?: number;
  invocationMetadata?: InvocationMetadataMap;
  historyMutation?: HistoryMutation;
  surfaceCommitToken?: string;
  onSurfacePaintReady?: (token: string, outcome: "ready" | "degraded") => void;
};

export function Transcript(props: TranscriptProps) {
  const {
    items, live: liveProp, liveStore, tabId, geometrySessionKey, footerHeight = 0,
    onPrompt, onDeliveryContinue, onAcceptDelivery, onOpenChanges, onOpenVerification,
    onEditPrompt, onRewind, checkpoints = EMPTY_CHECKPOINTS, actionPending = false,
    rewindDisabled = false, running = false, questionNavigator = true,
    welcomeVariant = "default", creationMode = false, actionHoverMenus = false,
    rewindSignal = 0, revealSignal = 0, hydrating = false, hasOlderHistory = false,
    historyStartTurn = 0, historyTotalTurns = 0, loadingOlderHistory = false,
    olderHistoryError, onLoadOlderHistory, turnStartAt, contentRevision = 0,
    invocationMetadata = EMPTY_INVOCATION_METADATA, historyMutation,
    surfaceCommitToken, onSurfacePaintReady,
  } = props;
  const t = useT();
  const subscribeLive = useCallback((listener: () => void) => liveStore?.subscribe(tabId, listener) ?? (() => {}), [liveStore, tabId]);
  const getLiveSnapshot = useCallback(() => liveStore?.getSnapshot(tabId) ?? liveProp, [liveProp, liveStore, tabId]);
  const live = useSyncExternalStore(subscribeLive, getLiveSnapshot, getLiveSnapshot);
  const resolvedSessionKey = geometrySessionKey || `tab:${tabId ?? "preview"}`;
  const surfaceKey = `${resolvedSessionKey}:${revealSignal}`;
  const entranceRef = useTranscriptEntranceAnimation<HTMLDivElement>(tabId, revealSignal, items);
  const viewportRef = useRef<TranscriptViewportHandle>(null);
  const committedSurfaceRef = useRef("");
  const experience = useSessionExperience();
  const liveFlags = useMemo<TranscriptLiveFlags>(() => live?.id ? {
    id: live.id,
    hasAnswerText: Boolean(live.text.trim()),
    hasReasoning: Boolean(live.reasoning),
    reasoningComplete: live.reasoningComplete,
  } : NO_LIVE, [live?.id, live?.reasoning, live?.reasoningComplete, live?.text]);
  const turnModels = useMemo(() => buildTurnModels(items, liveFlags, running, false), [items, liveFlags, running]);
  const kernel = useTranscriptKernel({
    sessionKey: surfaceKey,
    geometryRevision: `${contentRevision}:${footerHeight}:${experience}:${historyMutation?.seq ?? 0}`,
  });
  const [
    questions, loadedByTurn, totalQuestions, activeQuestion, setActiveQuestion,
    scheduleActiveQuestionSync, turnForUser, lastTurn,
  ] = useTranscriptQuestions(items, historyStartTurn, historyTotalTurns, kernel.scrollElement, kernel.scrollToBottom);

  const segmentStates = useMemo(() => foldSegmentStates(turnModels, experience === "deep"), [experience, turnModels]);
  const [folds, setFolds] = useState<FoldMap>(EMPTY_FOLDS);
  const experienceRef = useRef(experience);
  const foldSurfaceRef = useRef("");
  useLayoutEffect(() => {
    if (foldSurfaceRef.current === resolvedSessionKey) return;
    foldSurfaceRef.current = resolvedSessionKey;
    setFolds(readTranscriptFoldOverrides(resolvedSessionKey, segmentStates));
  }, [resolvedSessionKey, segmentStates]);
  useEffect(() => onSessionExperienceWillChange(() => {
    kernel.beginStructural("display-change");
  }), [kernel.beginStructural]);
  useEffect(() => {
    const preferenceChanged = experienceRef.current !== experience;
    experienceRef.current = experience;
    setFolds((previous) => {
      const next = reconcileFoldEntries(previous, segmentStates, experience, preferenceChanged);
      if (next) replaceTranscriptFoldOverrides(resolvedSessionKey, next);
      return next ?? previous;
    });
  }, [experience, resolvedSessionKey, segmentStates]);

  const subcallsByParent = useMemo(() => {
    const grouped = new Map<string, ToolItem[]>();
    for (const item of items) {
      if (item.kind !== "tool" || !item.parentId) continue;
      const children = grouped.get(item.parentId) ?? [];
      children.push(item);
      grouped.set(item.parentId, children);
    }
    return grouped;
  }, [items]);
  const checkpointsByTurn = useMemo(() => new Map(checkpoints.map((checkpoint) => [checkpoint.turn, checkpoint])), [checkpoints]);
  const blocks = useMemo(() => buildTranscriptRowBlocks(turnModels, {
    folds,
    sessionExperience: experience,
    hasOlderHistory: false,
    creationMode,
    turnForUser,
    hasCheckpointForTurn: (turn) => checkpointsByTurn.has(turn),
    subcallsByParent,
  }), [checkpointsByTurn, creationMode, experience, folds, subcallsByParent, turnForUser, turnModels]);
  const projection = useMemo(() => projectTranscriptTimeline(blocks, hasOlderHistory), [blocks, hasOlderHistory]);
  const renderMode = transcriptRenderMode(projection.completedBlocks.length, kernel.safeMode);
  const allRows = useMemo(() => blocks.flatMap((block) => block.rows), [blocks]);
  const empty = items.length === 0;
  const rowIndexByKey = useMemo(() => new Map(allRows.map((row, index) => [String(row.key), index])), [allRows]);
  const [selectableRows, liveSelectableRows] = useTranscriptSelectableRows(allRows, live);
  const selectionRetention = useTranscriptSelectionRetention({
    tabId,
    revealSignal,
    rowIndexByKey,
    selectableRows,
    selectableRowOverrides: liveSelectableRows,
    scrollRef: kernel.scrollRef,
    setScrollMode: kernel.setScrollMode,
    writeOffset: kernel.writeOffset,
    cancelStreamingScroll: () => kernel.beginGesture("selection"),
  });

  const handleFoldToggle = useCallback((segmentKey: string, open: boolean) => {
    kernel.beginStructural("display-change");
    setFolds((previous) => {
      const next = foldMapWithToggle(previous, segmentKey, open);
      const entry = next.get(segmentKey);
      if (entry) writeTranscriptFoldOverride(resolvedSessionKey, segmentKey, entry);
      return next;
    });
  }, [kernel.beginStructural, resolvedSessionKey]);
  const handleReasoningManualOpen = useCallback((segmentKey: string) => {
    kernel.beginStructural("display-change");
    const active = segmentStates.find((segment) => segment.key === segmentKey)?.hasRunningWork ?? false;
    setFolds((previous) => {
      const next = foldMapWithReasoningOpen(previous, segmentKey, active);
      const entry = next.get(segmentKey);
      if (entry) writeTranscriptFoldOverride(resolvedSessionKey, segmentKey, entry);
      return next;
    });
  }, [kernel.beginStructural, resolvedSessionKey, segmentStates]);
  const renderRow = useTranscriptRowRenderer({
    tabId, checkpoints, subcallsByParent, creationMode, running, actionPending,
    rewindDisabled, actionHoverMenus, turnStartAt, lastTurn,
    onFoldToggle: handleFoldToggle, onReasoningManualOpen: handleReasoningManualOpen,
    onPrompt, onDeliveryContinue, onAcceptDelivery, onOpenChanges, onOpenVerification,
    onEditPrompt, onRewind,
  });

  const [pendingQuestion, setPendingQuestion] = useState<QuestionAnchor | null>(null);
  const failedQuestionRef = useRef<QuestionAnchor | null>(null);
  const historyRequestRef = useRef(false);
  const requestOlder = useCallback(async (targetTurn?: number, trigger: HistoryLoadTrigger = "viewport-user") => {
    if (!onLoadOlderHistory || !hasOlderHistory || loadingOlderHistory || running || historyRequestRef.current) return false;
    historyRequestRef.current = true;
    if (trigger !== "question-jump" && trigger !== "retry") kernel.beginStructural("prepend");
    try {
      return await Promise.resolve(onLoadOlderHistory(targetTurn, trigger));
    } catch {
      return false;
    } finally {
      historyRequestRef.current = false;
    }
  }, [hasOlderHistory, kernel.beginStructural, loadingOlderHistory, onLoadOlderHistory, running]);
  const jumpToLoadedQuestion = useCallback((question: QuestionAnchor) => {
    const block = blocks.find((candidate) => candidate.questionAnchor === `u:${question.id}`);
    if (!block) return false;
    document.getSelection()?.removeAllRanges();
    selectionRetention.clear("question-navigation");
    setActiveQuestion(question.turn);
    viewportRef.current?.mountBlock(block.key);
    return kernel.jumpToBlock(block.key);
  }, [blocks, kernel.jumpToBlock, selectionRetention.clear, setActiveQuestion]);
  const handleJumpToQuestion = useCallback((question: QuestionAnchor) => {
    if (question.loaded !== false && jumpToLoadedQuestion(question)) return;
    failedQuestionRef.current = null;
    setPendingQuestion(question);
    void requestOlder(question.turn + 1, "question-jump").then((loaded) => {
      if (loaded) return;
      failedQuestionRef.current = question;
      setPendingQuestion((current) => current?.turn === question.turn ? null : current);
    });
  }, [jumpToLoadedQuestion, requestOlder]);
  useEffect(() => {
    if (!pendingQuestion || loadingOlderHistory) return;
    if (olderHistoryError) {
      failedQuestionRef.current = pendingQuestion;
      setPendingQuestion(null);
      return;
    }
    const loaded = loadedByTurn.get(pendingQuestion.turn);
    if (loaded && jumpToLoadedQuestion(loaded)) {
      failedQuestionRef.current = null;
      setPendingQuestion(null);
      return;
    }
    if (hasOlderHistory) void requestOlder(pendingQuestion.turn + 1, "question-jump");
  }, [hasOlderHistory, jumpToLoadedQuestion, loadedByTurn, loadingOlderHistory, olderHistoryError, pendingQuestion, requestOlder]);
  useEffect(() => {
    if (rewindSignal <= 0) return;
    const last = questions[questions.length - 1];
    if (last) jumpToLoadedQuestion(last);
  }, [jumpToLoadedQuestion, questions, rewindSignal]);

  const handleScroll = useCallback(() => {
    kernel.onScroll();
    scheduleActiveQuestionSync();
    const element = kernel.scrollRef.current;
    if (element && element.scrollTop <= 64 && kernel.kernel.userGestureActive) void requestOlder(undefined, "viewport-user");
  }, [kernel.kernel, kernel.onScroll, kernel.scrollRef, requestOlder, scheduleActiveQuestionSync]);
  const {
    state: creationScrollbar,
    handleScroll: handleCreationScroll,
    onThumbPointerDown: handleCreationScrollbarThumbPointerDown,
    onRailPointerDown: handleCreationScrollbarRailPointerDown,
  } = useCreationTranscriptScrollbar({
    enabled: creationMode,
    contentRevision,
    scrollRef: kernel.scrollRef,
    onScroll: handleScroll,
    setScrollMode: kernel.setScrollMode,
    writeOffset: kernel.writeOffset,
    finishProgrammaticScroll: kernel.endGesture,
  });

  const setScroller = useCallback((node: HTMLDivElement | null) => {
    kernel.setScroller(node);
    entranceRef.current = node;
  }, [entranceRef, kernel.setScroller]);
  const previousFooterHeight = useRef(footerHeight);
  useLayoutEffect(() => {
    if (previousFooterHeight.current === footerHeight) return;
    previousFooterHeight.current = footerHeight;
    kernel.beginStructural("composer-resize");
    kernel.commitViewportGeometry();
  }, [footerHeight, kernel.beginStructural, kernel.commitViewportGeometry]);

  useEffect(() => {
    acquireMarkdownWorkerClient();
    return () => releaseMarkdownWorkerClient();
  }, []);
  useEffect(() => {
    const parent = kernel.scrollElement;
    if (!parent) return;
    return attachNestedScrollHandoff({
      parent,
      onParentScrollIntent: () => kernel.onWheelCapture(),
      writeParentOffset: (top) => kernel.writeOffset("nested-scroll", top),
    }).detach;
  }, [kernel.onWheelCapture, kernel.scrollElement, kernel.writeOffset]);
  useEffect(() => {
    recordFrontendDiagnostic("transcript", "transcript.surface", {
      generation: kernel.kernel.generation,
      completedBlocks: projection.completedBlocks.length,
      renderMode,
    });
  }, [kernel.kernel.generation, projection.completedBlocks.length, renderMode, surfaceKey]);
  useEffect(() => {
    if (!surfaceCommitToken || !onSurfacePaintReady || hydrating) return;
    const generation = kernel.kernel.generation;
    const commitKey = `${generation}:${surfaceCommitToken}`;
    if (committedSurfaceRef.current === commitKey) return;
    const frame = requestAnimationFrame(() => {
      const element = kernel.scrollRef.current;
      if (generation !== kernel.kernel.generation || !element) return;
      if (!empty && !element.querySelector("[data-transcript-block-key]")) return;
      if (committedSurfaceRef.current === commitKey) return;
      committedSurfaceRef.current = commitKey;
      onSurfacePaintReady(surfaceCommitToken, kernel.safeMode ? "degraded" : "ready");
    });
    return () => cancelAnimationFrame(frame);
  }, [empty, hydrating, kernel.kernel, kernel.safeMode, kernel.scrollRef, onSurfacePaintReady, projection, surfaceCommitToken]);
  const autoFillRef = useRef({ surface: "", pages: 0 });
  useEffect(() => {
    if (autoFillRef.current.surface !== surfaceKey) autoFillRef.current = { surface: surfaceKey, pages: 0 };
    if (hydrating || !hasOlderHistory || loadingOlderHistory || olderHistoryError || running || autoFillRef.current.pages >= 3) return;
    const frame = requestAnimationFrame(() => {
      const element = kernel.scrollRef.current;
      if (!element || element.clientHeight <= 0 || element.scrollHeight > element.clientHeight + 4) return;
      autoFillRef.current.pages += 1;
      void requestOlder(undefined, "auto-fill");
    });
    return () => cancelAnimationFrame(frame);
  }, [hasOlderHistory, hydrating, kernel.scrollRef, loadingOlderHistory, olderHistoryError, projection.completedBlocks.length, requestOlder, running, surfaceKey]);

  const showQuestionNav = questionNavigator && totalQuestions >= QUESTION_NAV_MIN_COUNT;
  const selectionSnapshot = useSyncExternalStore(transcriptSelectionStore.subscribe, transcriptSelectionStore.getSnapshot, transcriptSelectionStore.getSnapshot);
  const protectedBlockKeys = useMemo(() => {
    const keys = new Set<string>();
    if (kernel.kernel.anchor.kind === "block") keys.add(kernel.kernel.anchor.blockKey);
    const endpoints = selectionSnapshot.mode.startsWith("logical")
      ? [selectionSnapshot.anchor?.rowKey, selectionSnapshot.focus?.rowKey]
      : [];
    for (const block of blocks) {
      if (block.rows.some((row) => endpoints.includes(row.key))) keys.add(block.key);
    }
    return keys;
  }, [blocks, kernel.kernel.anchor, selectionSnapshot]);

  return (
    <InvocationMetadataContext.Provider value={invocationMetadata}>
    <MarkdownImageTabContext.Provider value={tabId ?? ""}>
    <TranscriptLayoutIntentProvider value={() => { kernel.beginStructural("display-change"); }}>
    <TranscriptScrollWriteProvider value={kernel.writeOffset}>
      <div className="transcript-shell" aria-busy={Boolean(pendingQuestion) || undefined} data-protected-blocks={protectedBlockKeys.size}>
        {empty ? (
          <div className={`transcript transcript--empty${creationMode ? " transcript--creation-scrollbar" : ""}`} ref={setScroller} aria-busy={hydrating || undefined}>
            {hydrating ? <div className="transcript__loading" role="status" aria-live="polite"><Loader2 className="transcript__loading-icon" aria-hidden="true" /><span>{t("common.loading")}</span></div>
              : <Welcome onPrompt={onPrompt} variant={welcomeVariant} />}
          </div>
        ) : (
          <LiveStreamContext.Provider value={live}>
            <div
              ref={setScroller}
              className={`transcript${creationMode ? " transcript--creation-scrollbar" : ""}${creationMode && creationScrollbar.hot ? " transcript--scrollbar-hot" : ""}`}
              data-transcript-hydrating={hydrating ? "true" : "false"}
              data-transcript-generation={kernel.kernel.generation}
              data-transcript-intent={kernel.intent}
              data-transcript-row-count={allRows.length}
              data-transcript-block-count={blocks.length}
              data-scroll-mode={selectionSnapshot.mode !== "none" ? "selection" : kernel.intent === "tail" ? "tail-follow" : "manual"}
              onScroll={creationMode ? handleCreationScroll : handleScroll}
              onWheelCapture={() => kernel.onWheelCapture()}
              onTouchStartCapture={() => kernel.onTouchStartCapture()}
              onTouchEndCapture={() => kernel.onTouchEndCapture()}
              onTouchCancelCapture={() => kernel.onTouchEndCapture()}
              onKeyDownCapture={kernel.onKeyDownCapture}
              onPointerDownCapture={(event) => {
                kernel.onPointerDownCapture(event);
                selectionRetention.onPointerDownCapture(event);
              }}
            >
              <TranscriptViewport
                key={surfaceKey}
                ref={viewportRef}
                projection={projection}
                mode={renderMode}
                tabId={tabId}
                scrollElement={kernel.scrollElement}
                renderRow={renderRow}
                loadingOlderHistory={loadingOlderHistory}
                olderHistoryError={olderHistoryError}
                onRetryOlderHistory={() => {
                  const question = pendingQuestion ?? failedQuestionRef.current;
                  if (question) setPendingQuestion(question);
                  void requestOlder(question ? question.turn + 1 : undefined, "retry");
                }}
                onGeometryWillChange={kernel.beginAnchorRestore}
                onGeometryChange={kernel.commitViewportGeometry}
                onAnomaly={kernel.reportAnomaly}
                onGeometryHealthy={kernel.reportHealthyGeometry}
                kernel={kernel.kernel}
                protectedBlockKeys={protectedBlockKeys}
                running={running}
                turnStartAt={turnStartAt}
              />
            </div>
          </LiveStreamContext.Provider>
        )}
        {creationMode && creationScrollbar.visible && <div className={`transcript__scrollbar${creationScrollbar.hot ? " transcript__scrollbar--hot" : ""}`} onPointerDown={handleCreationScrollbarRailPointerDown} aria-hidden="true">
          <div className="transcript__scrollbar-thumb" style={{ top: creationScrollbar.thumbTop, height: creationScrollbar.thumbHeight } as CSSProperties} onPointerDown={handleCreationScrollbarThumbPointerDown} />
        </div>}
        {!empty && showQuestionNav && <Suspense fallback={null}><QuestionJumpBar loadedQuestions={questions} totalQuestions={totalQuestions} activeTurn={activeQuestion} onJump={handleJumpToQuestion} /></Suspense>}
        {!empty && !kernel.isAtBottom && kernel.scrollElement && hasTranscriptScrollableRange(kernel.scrollElement) && <button type="button" className="transcript__jump-bottom" onClick={() => { selectionRetention.endStaleGesture(); kernel.scrollToBottom(); }} aria-label={t("transcript.jumpToBottom")} title={t("transcript.jumpToBottom")}><ArrowDown size={18} strokeWidth={2.2} aria-hidden="true" /></button>}
        {pendingQuestion && <div className="transcript-navigation-overlay transcript-question-jump-overlay" data-question-jump-mask="true" data-question-jump-phase="loading" role="status" aria-live="polite"><span className="transcript-navigation-overlay__spinner" aria-hidden="true" /><span>{t("common.loading")}</span></div>}
        {FrontendDiagnosticsPanel && <Suspense fallback={null}><FrontendDiagnosticsPanel scrollElement={kernel.scrollElement} totalRows={allRows.length} /></Suspense>}
      </div>
    </TranscriptScrollWriteProvider>
    </TranscriptLayoutIntentProvider>
    </MarkdownImageTabContext.Provider>
    </InvocationMetadataContext.Provider>
  );
}
