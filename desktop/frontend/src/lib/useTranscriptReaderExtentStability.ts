import { useCallback, useEffect, useMemo, useRef, type RefObject } from "react";
import {
  acceptTranscriptReaderExtentCorrection,
  createTranscriptReaderExtentGuard,
  extendTranscriptReaderExtentGuard,
  MIN_REVERSE_JUMP_PX,
  observeTranscriptReaderExtent,
  resolveTranscriptReaderExtentCorrection,
  transcriptReaderBlankForwardDelta,
  transcriptReaderAnchorReverseDelta,
  transcriptReaderExtentCanCorrect,
  transcriptReaderExtentHasCollapsed,
  transcriptReaderExtentReverseDelta,
  type TranscriptExtentSnapshot,
  type TranscriptReaderExtentGuard,
} from "./transcriptReaderExtentStability";
import type { TranscriptScrollMode } from "./transcriptScrollArbiter";
import { nativeTranscriptDistanceFromBottom } from "./transcriptScrollGeometry";
import { recordTranscriptScrollDiagnostic, type TranscriptScrollWriteRecord } from "./transcriptScrollProbe";
import { shouldBridgeTranscriptReaderCorrection } from "./transcriptScrollWriter";
import { captureLeadingTranscriptLayoutAnchor, transcriptElementViewportIsBlank } from "./transcriptVirtuosoRecovery";

const READER_EXTENT_ACTIVE_MS = 180;
// WebView2 can coalesce a sustained native wheel burst and commit Virtuoso's
// replacement range after the reader-intent idle timer has fired. Retain the
// last accepted logical row passively across that bounded compositor delay;
// ownership changes still cancel immediately and ordinary sub-96px layout
// jitter never earns a correction.
// Native WebViews may commit a queued Virtuoso range several seconds after
// the wheel event that selected it (notably while the host process is also
// measuring streamed rows). Keep this lease passive after 180ms: it owns no
// frame loop or scroll position, but a pre-paint mutation/resize observation
// can still reject a late replacement range. Explicit ownership changes
// cancel it immediately.
const READER_EXTENT_RETENTION_MS = 5_000;

type ActiveReaderExtentGuard = TranscriptReaderExtentGuard & {
  element: HTMLDivElement;
  generation: number;
  deadline: number;
  activeFrameDeadline: number;
  frame: number | null;
  paintFrame: number | null;
  paintTimer: number | null;
  expiryTimer: number | null;
  pendingCorrectionTop?: number;
  pendingCorrectionForward?: boolean;
  pendingAnchor?: readonly [rowKey: string, offsetAtTarget: number];
  paintedRows: ReadonlyMap<string, number>;
  previousPaintedRows?: ReadonlyMap<string, number>;
};

type PaintedReaderReverse = {
  delta: number;
  rowKey: string;
  currentOffset: number;
};

function capturePaintedReaderRows(element: HTMLDivElement): ReadonlyMap<string, number> {
  const viewport = element.getBoundingClientRect();
  const rows = new Map<string, number>();
  for (const row of element.querySelectorAll<HTMLElement>(".transcript__row[data-row-key]")) {
    const rowKey = row.dataset.rowKey;
    const rect = row.getBoundingClientRect();
    if (rowKey && rect.bottom > viewport.top && rect.top < viewport.bottom) {
      rows.set(rowKey, rect.top - viewport.top);
    }
  }
  return rows;
}

function promotePaintedReaderRows(
  guard: ActiveReaderExtentGuard,
  next: ReadonlyMap<string, number>,
) {
  let commonRows = 0;
  for (const rowKey of next.keys()) {
    if (guard.paintedRows.has(rowKey)) commonRows += 1;
  }
  // Mutation, resize, scroll, and rAF observers can all promote the same
  // mounted Virtuoso window before the native smoke sampler reaches its next
  // painted frame. Refresh that window's offsets without rotating away the
  // preceding logical range. Only a majority range replacement advances the
  // two-window history.
  if (commonRows * 2 < Math.min(guard.paintedRows.size, next.size)) {
    guard.previousPaintedRows = guard.paintedRows;
  }
  guard.paintedRows = next;
}

function paintedReaderReverse(
  guard: ActiveReaderExtentGuard,
  element: HTMLDivElement,
): PaintedReaderReverse | undefined {
  const current = capturePaintedReaderRows(element);
  let strongest: PaintedReaderReverse | undefined;
  for (const paintedRows of [guard.paintedRows, guard.previousPaintedRows]) {
    if (!paintedRows) continue;
    const common = [...current].flatMap(([rowKey, currentOffset]) => {
      const previousOffset = paintedRows.get(rowKey);
      return previousOffset === undefined
        ? []
        : [{ delta: guard.direction * (currentOffset - previousOffset), rowKey, currentOffset }];
    }).sort((left, right) => left.delta - right.delta);
    const candidate = common[Math.floor(common.length / 2)];
    if (candidate && (!strongest || candidate.delta > strongest.delta)) strongest = candidate;
  }
  return strongest;
}

function readerAnchorOffset(element: HTMLDivElement, rowKey: string): number | undefined {
  const row = Array.from(element.querySelectorAll<HTMLElement>(".transcript__row[data-row-key]"))
    .find((candidate) => candidate.dataset.rowKey === rowKey);
  return row ? row.getBoundingClientRect().top - element.getBoundingClientRect().top : undefined;
}

export function useTranscriptReaderExtentStability({
  generationRef,
  modeRef,
  scrollRef,
  writeCorrection,
  lastWriteOwner,
}: {
  generationRef: RefObject<number>;
  modeRef: RefObject<TranscriptScrollMode>;
  scrollRef: RefObject<HTMLDivElement | null>;
  writeCorrection: (write: TranscriptScrollWriteRecord) => boolean;
  lastWriteOwner: () => string | undefined;
}) {
  const guardRef = useRef<ActiveReaderExtentGuard | null>(null);
  const lastNativeDeliveryRef = useRef<{
    element: HTMLDivElement;
    generation: number;
    height: number;
    top: number;
  } | null>(null);

  const clearActive = useCallback((guard: ActiveReaderExtentGuard) => {
    if (guardRef.current === guard) guardRef.current = null;
    if (guard.frame != null) cancelAnimationFrame(guard.frame);
    if (guard.paintFrame != null) cancelAnimationFrame(guard.paintFrame);
    if (guard.paintTimer != null) window.clearTimeout(guard.paintTimer);
    if (guard.expiryTimer != null) window.clearTimeout(guard.expiryTimer);
    guard.frame = null;
    guard.paintFrame = null;
    guard.paintTimer = null;
    guard.expiryTimer = null;
  }, []);

  const cancel = useCallback(() => {
    const guard = guardRef.current;
    if (guard) clearActive(guard);
  }, [clearActive]);

  const renewLease = useCallback((guard: ActiveReaderExtentGuard) => {
    const now = Date.now();
    guard.activeFrameDeadline = now + READER_EXTENT_ACTIVE_MS;
    guard.deadline = now + READER_EXTENT_RETENTION_MS;
    if (guard.expiryTimer != null) window.clearTimeout(guard.expiryTimer);
    guard.expiryTimer = window.setTimeout(() => clearActive(guard), READER_EXTENT_RETENTION_MS);
  }, [clearActive]);

  const isActive = useCallback(() => guardRef.current !== null, []);

  const anchorOffset = useCallback((guard: ActiveReaderExtentGuard, element: HTMLDivElement) => {
    return guard.anchor ? readerAnchorOffset(element, guard.anchor.rowKey) : undefined;
  }, []);

  const commitPaintedRowsAfterPaint = useCallback((guard: ActiveReaderExtentGuard, element: HTMLDivElement) => {
    if (guard.paintFrame !== null || guard.paintTimer !== null) return;
    guard.paintFrame = requestAnimationFrame(() => {
      guard.paintFrame = null;
      // A Virtuoso range replacement can pass through multiple mutation
      // records in one rendering opportunity. Promote the candidate only
      // after that opportunity has painted; otherwise a transient range with
      // no common rows can erase the last user-visible baseline before the
      // final range restores one boundary row.
      guard.paintTimer = window.setTimeout(() => {
        guard.paintTimer = null;
        if (
          guardRef.current !== guard
          || generationRef.current !== guard.generation
          || scrollRef.current !== element
          || Date.now() >= guard.deadline
          || transcriptElementViewportIsBlank(element)
        ) return;
        promotePaintedReaderRows(guard, capturePaintedReaderRows(element));
      }, 0);
    });
  }, [generationRef, scrollRef]);

  const acknowledgeCorrection = useCallback((guard: ActiveReaderExtentGuard, element: HTMLDivElement, snapshot: TranscriptExtentSnapshot) => {
    if (guard.pendingCorrectionTop === undefined) return;
    const progressPastTarget = guard.direction * (snapshot.scrollTop - guard.pendingCorrectionTop);
    const passedForwardCorrection = progressPastTarget > guard.clientHeight
      && guard.pendingCorrectionForward === true;
    if (progressPastTarget < -2 || (progressPastTarget > guard.clientHeight && !passedForwardCorrection)) return;
    const pendingAnchor = guard.pendingAnchor;
    if (!passedForwardCorrection && pendingAnchor) {
      const currentOffset = readerAnchorOffset(element, pendingAnchor[0]);
      if (currentOffset !== undefined) {
        const expectedOffset = pendingAnchor[1] - (snapshot.scrollTop - guard.pendingCorrectionTop);
        if (guard.direction * (currentOffset - expectedOffset) >= MIN_REVERSE_JUMP_PX) {
          // The native offset acknowledged the write in the same delivery
          // that committed another older Virtuoso range. Keep the correction
          // anchor long enough for the ordinary anomaly path below to reject
          // that range; blessing its leading row here would make the visual
          // reversal the next transaction's baseline.
          guard.anchor = { mode: "manual", rowKey: pendingAnchor[0], offset: pendingAnchor[1] };
          guard.anchorScrollTop = guard.pendingCorrectionTop;
          guard.targetAnchorOffset = pendingAnchor[1];
          return;
        }
      }
    }
    guard.pendingCorrectionTop = undefined;
    guard.pendingCorrectionForward = undefined;
    guard.pendingAnchor = undefined;
    if (passedForwardCorrection && !transcriptElementViewportIsBlank(element)) {
      // Once real native input has moved more than a viewport beyond a
      // stalled forward correction, the rows captured before that correction
      // are no longer an adjacent painted frame. Comparing a newly mounted
      // range with that stale map creates a correction staircase on WebViews.
      // Rebase the passive visual guard here; a later mutation is still
      // compared with this current occupied range before it can paint.
      guard.paintedRows = capturePaintedReaderRows(element);
      guard.previousPaintedRows = undefined;
    }
    // A correction intentionally drops the stale pre-swap anchor. Re-anchor
    // as soon as the native offset reaches or passes that correction in the
    // gesture direction. Native hosts can coalesce the next wheel delta with
    // the acknowledgement (for example target+24), and exact equality would
    // leave the following visual range replacement protected by scrollTop alone.
    const anchor = captureLeadingTranscriptLayoutAnchor(element);
    if (!anchor) return;
    guard.anchor = anchor;
    guard.anchorScrollTop = snapshot.scrollTop;
    guard.targetAnchorOffset = anchor.offset;
  }, []);

  const reportAnomaly = useCallback((
    guard: ActiveReaderExtentGuard,
    element: HTMLDivElement,
    currentAnchorOffset?: number,
    paintedReverse?: PaintedReaderReverse,
  ) => {
    const reverseDelta = Math.max(
      transcriptReaderExtentReverseDelta(guard, element),
      transcriptReaderAnchorReverseDelta(guard, element, currentAnchorOffset),
      transcriptReaderBlankForwardDelta(guard),
      paintedReverse?.delta ?? 0,
    );
    if (reverseDelta < MIN_REVERSE_JUMP_PX || guard.anomalyReported) return;
    guard.anomalyReported = true;
    recordTranscriptScrollDiagnostic("scroll-anomaly", {
      source: "reader-gesture",
      mode: modeRef.current,
      owner: lastWriteOwner(),
      direction: guard.direction > 0 ? "down" : "up",
      reverseDelta,
      extentDelta: element.scrollHeight - guard.acceptedHeight,
      scrollTop: element.scrollTop,
      scrollHeight: element.scrollHeight,
      clientHeight: element.clientHeight,
      bottomDistance: nativeTranscriptDistanceFromBottom(element),
      waiting: true,
      corrected: false,
    });
  }, [lastWriteOwner, modeRef]);

  const correctAnomaly = useCallback((
    active: ActiveReaderExtentGuard,
    element: HTMLDivElement,
    snapshot: TranscriptExtentSnapshot,
    currentAnchorOffset?: number,
    paintedReverse?: PaintedReaderReverse,
  ) => {
    reportAnomaly(active, element, currentAnchorOffset, paintedReverse);
    // Extent collapse/rebound owns the logical target. A smaller displacement
    // from rows painted while the native range was clamped must not replace
    // the pre-collapse accepted position with that transient viewport.
    const paintedCorrection = !active.collapsed
      && paintedReverse && paintedReverse.delta >= MIN_REVERSE_JUMP_PX
      ? active.direction * paintedReverse.delta
      : undefined;
    if (
      paintedCorrection === undefined
      && !transcriptReaderExtentCanCorrect(active, snapshot, currentAnchorOffset)
    ) return false;
    const rawCorrection = paintedCorrection
      ?? resolveTranscriptReaderExtentCorrection(active, snapshot, currentAnchorOffset);
    const maxTop = Math.max(0, snapshot.scrollHeight - snapshot.clientHeight);
    const correctionTarget = rawCorrection === undefined
      ? undefined
      : Math.max(0, Math.min(maxTop, snapshot.scrollTop + rawCorrection));
    const correction = correctionTarget === undefined ? undefined : correctionTarget - snapshot.scrollTop;
    const mode = modeRef.current;
    if (
      correctionTarget !== undefined
      && active.pendingCorrectionTop !== undefined
      && Math.abs(correctionTarget - active.pendingCorrectionTop) <= 2
    ) return true;
    if (correction === undefined || !writeCorrection({
      owner: "reader-stability",
      kind: "scrollBy",
      top: correction,
      source: "layout-height-changed",
      scrollTop: element.scrollTop,
      scrollHeight: element.scrollHeight,
      clientHeight: element.clientHeight,
      bottomDistance: nativeTranscriptDistanceFromBottom(element),
      mode,
    })) return false;
    active.pendingCorrectionTop = correctionTarget;
    active.pendingCorrectionForward = active.direction * correction > 0;
    active.pendingAnchor = paintedReverse
      ? [paintedReverse.rowKey, paintedReverse.currentOffset - correction]
      : active.anchor && currentAnchorOffset !== undefined
        ? [active.anchor.rowKey, currentAnchorOffset - correction]
        : undefined;
    const extentDelta = snapshot.scrollHeight - active.acceptedHeight;
    acceptTranscriptReaderExtentCorrection(active, snapshot, correction);
    recordTranscriptScrollDiagnostic("scroll-anomaly", {
      source: "reader-gesture",
      mode,
      owner: lastWriteOwner(),
      direction: active.direction > 0 ? "down" : "up",
      reverseDelta: Math.abs(correction),
      extentDelta,
      scrollTop: snapshot.scrollTop,
      scrollHeight: snapshot.scrollHeight,
      clientHeight: snapshot.clientHeight,
      bottomDistance: nativeTranscriptDistanceFromBottom(element),
      waiting: false,
      corrected: true,
    });
    return true;
  }, [lastWriteOwner, modeRef, reportAnomaly, writeCorrection]);

  const observe = useCallback((element = scrollRef.current) => {
    const guard = guardRef.current;
    if (!element || guard?.element !== element) return false;
    const now = Date.now();
    const mode = modeRef.current;
    if (
      now >= guard.deadline
      || (
        mode !== "reader-gesture"
        && mode !== "manual"
        && (mode !== "tail-follow" || now >= guard.activeFrameDeadline)
      )
    ) {
      clearActive(guard);
      return false;
    }
    const snapshot = {
      scrollTop: element.scrollTop,
      scrollHeight: element.scrollHeight,
      clientHeight: element.clientHeight,
    };
    acknowledgeCorrection(guard, element, snapshot);
    const viewportBlank = transcriptElementViewportIsBlank(element);
    const currentAnchorOffset = anchorOffset(guard, element);
    const previousAcceptedTop = guard.acceptedTop;
    const paintedReverse = viewportBlank ? undefined : paintedReaderReverse(guard, element);
    let corrected = paintedReverse && paintedReverse.delta >= MIN_REVERSE_JUMP_PX
      ? correctAnomaly(guard, element, snapshot, currentAnchorOffset, paintedReverse)
      : false;
    const accepted = corrected
      ? false
      : observeTranscriptReaderExtent(guard, snapshot, currentAnchorOffset, viewportBlank);
    // An unpainted Virtuoso range cannot supply a trustworthy visual anchor,
    // but a native extent reversal/collapse can still be corrected from the
    // last accepted logical position.
    corrected = corrected || correctAnomaly(guard, element, snapshot, viewportBlank ? undefined : currentAnchorOffset);
    if (
      accepted
      && (
        guard.direction * (snapshot.scrollTop - previousAcceptedTop) > 2
        || !guard.anchor
        || currentAnchorOffset === undefined
      )
      && !corrected
      && guard.pendingCorrectionTop === undefined
    ) {
      const anchor = captureLeadingTranscriptLayoutAnchor(element);
      if (anchor) {
        // Commit the row that was actually painted for this accepted frame.
        // The next Virtuoso range swap is compared with this row rather than
        // a possibly unmounted row from the beginning of a long gesture.
        guard.anchor = anchor;
        guard.anchorScrollTop = snapshot.scrollTop;
        guard.targetAnchorOffset = anchor.offset;
      }
    }
    if (accepted && !corrected && !viewportBlank) commitPaintedRowsAfterPaint(guard, element);
    return transcriptReaderExtentHasCollapsed(guard);
  }, [acknowledgeCorrection, anchorOffset, clearActive, commitPaintedRowsAfterPaint, correctAnomaly, modeRef, scrollRef]);

  const schedule = useCallback((active: ActiveReaderExtentGuard) => {
    if (active.frame !== null) return;
    const tick = () => {
      active.frame = null;
      const mode = modeRef.current;
      if (
        guardRef.current !== active
        || generationRef.current !== active.generation
        || scrollRef.current !== active.element
        || (
          mode !== "reader-gesture"
          && mode !== "manual"
          && (mode !== "tail-follow" || Date.now() >= active.activeFrameDeadline)
        )
      ) {
        if (guardRef.current === active) clearActive(active);
        return;
      }
      const element = active.element;
      const snapshot = {
        scrollTop: element.scrollTop,
        scrollHeight: element.scrollHeight,
        clientHeight: element.clientHeight,
      };
      acknowledgeCorrection(active, element, snapshot);
      const viewportBlank = transcriptElementViewportIsBlank(element);
      const currentAnchorOffset = anchorOffset(active, element);
      const previousAcceptedTop = active.acceptedTop;
      const paintedReverse = viewportBlank ? undefined : paintedReaderReverse(active, element);
      let corrected = paintedReverse && paintedReverse.delta >= MIN_REVERSE_JUMP_PX
        ? correctAnomaly(active, element, snapshot, currentAnchorOffset, paintedReverse)
        : false;
      const accepted = corrected
        ? false
        : observeTranscriptReaderExtent(active, snapshot, currentAnchorOffset, viewportBlank);
      corrected = corrected || correctAnomaly(active, element, snapshot, viewportBlank ? undefined : currentAnchorOffset);
      if (
        accepted
        && (
          active.direction * (snapshot.scrollTop - previousAcceptedTop) > 2
          || !active.anchor
          || currentAnchorOffset === undefined
        )
        && !corrected
        && active.pendingCorrectionTop === undefined
      ) {
        const anchor = captureLeadingTranscriptLayoutAnchor(element);
        if (anchor) {
          active.anchor = anchor;
          active.anchorScrollTop = snapshot.scrollTop;
          active.targetAnchorOffset = anchor.offset;
        }
      }
      if (accepted && !corrected && !viewportBlank) commitPaintedRowsAfterPaint(active, element);
      // After the ordinary 180ms active sampling window, keep the accepted
      // anchor as a passive lease. Mutation/resize/native-scroll observers can
      // still reject a late WebView range swap without spinning a frame loop.
      if (Date.now() >= active.activeFrameDeadline) return;
      active.frame = requestAnimationFrame(tick);
    };
    active.frame = requestAnimationFrame(tick);
  }, [acknowledgeCorrection, anchorOffset, clearActive, commitPaintedRowsAfterPaint, correctAnomaly, generationRef, modeRef, scrollRef]);

  const arm = useCallback((deltaY: number) => {
    const element = scrollRef.current;
    if (!element) return;
    const anchor = captureLeadingTranscriptLayoutAnchor(element);
    const current = guardRef.current;
    const extensionAnchor = current?.anchor
      && anchorOffset(current, element) !== undefined
      && anchor?.mode === "manual"
      && anchor.rowKey !== current.anchor.rowKey
      ? undefined
      : anchor;
    if (
      current
      && current.element === element
      && current.generation === generationRef.current
      && extendTranscriptReaderExtentGuard(current, element, extensionAnchor, deltaY)
    ) {
      renewLease(current);
      schedule(current);
      return;
    }

    cancel();
    const guard = createTranscriptReaderExtentGuard(element, anchor, deltaY);
    if (!guard) return;
    const active: ActiveReaderExtentGuard = {
      ...guard,
      element,
      generation: generationRef.current,
      deadline: 0,
      activeFrameDeadline: 0,
      frame: null,
      paintFrame: null,
      paintTimer: null,
      expiryTimer: null,
      paintedRows: capturePaintedReaderRows(element),
    };
    guardRef.current = active;
    renewLease(active);
    schedule(active);
  }, [anchorOffset, cancel, generationRef, renewLease, schedule, scrollRef]);

  const syncNativeDirection = useCallback((deltaY: number) => {
    const current = guardRef.current;
    if (
      current
      && current.element === scrollRef.current
      && current.generation === generationRef.current
      && current.direction === (deltaY < 0 ? -1 : 1)
    ) {
      renewLease(current);
      schedule(current);
      return;
    }
    arm(deltaY);
    const next = guardRef.current;
    if (next) {
      // This path observes a delivery that has already moved the native
      // scroller. Unlike a pre-scroll wheel intent, its current top/anchor is
      // the expectation; adding deltaY again would create an overshoot.
      next.expectedTop = next.element.scrollTop;
      next.anchorScrollTop = next.anchor ? next.element.scrollTop : undefined;
      next.targetAnchorOffset = next.anchor?.offset;
    }
  }, [arm, generationRef, renewLease, schedule, scrollRef]);

  const observeNativeDelivery = useCallback((element: HTMLDivElement) => {
    const generation = generationRef.current;
    const previous = lastNativeDeliveryRef.current;
    const deliveredTop = element.scrollTop;
    const pendingBeforeObservation = guardRef.current?.pendingCorrectionTop !== undefined;
    const sameSurface = previous?.element === element && previous.generation === generation;
    const nativeDelta = sameSurface ? deliveredTop - previous.top : 0;
    const extentDelta = sameSurface ? element.scrollHeight - previous.height : 0;
    const layoutAnchored = Math.abs(extentDelta) > 2
      && Math.abs(nativeDelta - extentDelta) <= Math.max(8, element.clientHeight * 0.1);
    const view = element.ownerDocument.defaultView;
    const nativeInput = Math.abs(nativeDelta) > 2
      && Math.abs(nativeDelta) <= element.clientHeight
      && !layoutAnchored
      && (modeRef.current === "reader-gesture" || modeRef.current === "manual")
      && view
      && shouldBridgeTranscriptReaderCorrection(view)
      && !pendingBeforeObservation;
    // Reconcile a coalesced delivery before observing it. Otherwise an
    // opposite setup direction can misclassify the first >96px native move as
    // a reverse layout anomaly and write against real user input.
    if (nativeInput) syncNativeDirection(nativeDelta);
    observe(element);
    const active = guardRef.current;
    const pendingAfterObservation = active?.pendingCorrectionTop !== undefined;
    if (
      nativeInput
      && !pendingAfterObservation
      && active?.element === element
      && active.direction === (nativeDelta < 0 ? -1 : 1)
      && Math.abs(active.acceptedTop - element.scrollTop) <= 2
    ) active.expectedTop = element.scrollTop;
    lastNativeDeliveryRef.current = {
      element,
      generation,
      height: element.scrollHeight,
      top: element.scrollTop,
    };
  }, [generationRef, modeRef, observe, syncNativeDirection]);

  useEffect(() => cancel, [cancel]);

  return useMemo(
    () => ({ arm, cancel, observe, observeNativeDelivery, isActive }),
    [arm, cancel, observe, observeNativeDelivery, isActive],
  );
}
