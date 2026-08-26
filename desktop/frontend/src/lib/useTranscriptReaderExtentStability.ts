import { useCallback, useEffect, useMemo, useRef, type RefObject } from "react";
import {
  acceptTranscriptReaderExtentCorrection,
  createTranscriptReaderExtentGuard,
  extendTranscriptReaderExtentGuard,
  MIN_REVERSE_JUMP_PX,
  observeTranscriptReaderExtent,
  resolveTranscriptReaderExtentCorrection,
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
  expiryTimer: number | null;
  pendingCorrectionTop?: number;
};

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

  const clearActive = useCallback((guard: ActiveReaderExtentGuard) => {
    if (guardRef.current === guard) guardRef.current = null;
    if (guard.frame != null) cancelAnimationFrame(guard.frame);
    if (guard.expiryTimer != null) window.clearTimeout(guard.expiryTimer);
    guard.frame = null;
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
    const row = guard.anchor
      ? Array.from(element.querySelectorAll<HTMLElement>(".transcript__row[data-row-key]"))
        .find((candidate) => candidate.dataset.rowKey === guard.anchor?.rowKey)
      : undefined;
    return row ? row.getBoundingClientRect().top - element.getBoundingClientRect().top : undefined;
  }, []);

  const reportAnomaly = useCallback((
    guard: ActiveReaderExtentGuard,
    element: HTMLDivElement,
    currentAnchorOffset?: number,
  ) => {
    const reverseDelta = Math.max(
      transcriptReaderExtentReverseDelta(guard, element),
      transcriptReaderAnchorReverseDelta(guard, element, currentAnchorOffset),
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
  ) => {
    reportAnomaly(active, element, currentAnchorOffset);
    if (!transcriptReaderExtentCanCorrect(active, snapshot, currentAnchorOffset)) return false;
    const correction = resolveTranscriptReaderExtentCorrection(active, snapshot, currentAnchorOffset);
    const mode = modeRef.current;
    const correctionTarget = correction === undefined ? undefined : snapshot.scrollTop + correction;
    if (
      correctionTarget !== undefined
      && active.pendingCorrectionTop !== undefined
      && Math.abs(correctionTarget - active.pendingCorrectionTop) <= 2
    ) return false;
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
    if (Date.now() >= guard.deadline) {
      clearActive(guard);
      return false;
    }
    const snapshot = {
      scrollTop: element.scrollTop,
      scrollHeight: element.scrollHeight,
      clientHeight: element.clientHeight,
    };
    if (guard.pendingCorrectionTop !== undefined && Math.abs(snapshot.scrollTop - guard.pendingCorrectionTop) <= 2) {
      guard.pendingCorrectionTop = undefined;
    }
    const viewportBlank = transcriptElementViewportIsBlank(element);
    const currentAnchorOffset = anchorOffset(guard, element);
    observeTranscriptReaderExtent(guard, snapshot, currentAnchorOffset, viewportBlank);
    // An unpainted Virtuoso range cannot supply a trustworthy visual anchor,
    // but a native extent reversal/collapse can still be corrected from the
    // last accepted logical position.
    correctAnomaly(guard, element, snapshot, viewportBlank ? undefined : currentAnchorOffset);
    return transcriptReaderExtentHasCollapsed(guard);
  }, [anchorOffset, clearActive, correctAnomaly, scrollRef]);

  const schedule = useCallback((active: ActiveReaderExtentGuard) => {
    if (active.frame !== null) return;
    const tick = () => {
      active.frame = null;
      const mode = modeRef.current;
      if (
        guardRef.current !== active
        || generationRef.current !== active.generation
        || scrollRef.current !== active.element
        || (mode !== "reader-gesture" && mode !== "manual")
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
      if (active.pendingCorrectionTop !== undefined && Math.abs(snapshot.scrollTop - active.pendingCorrectionTop) <= 2) {
        active.pendingCorrectionTop = undefined;
      }
      const viewportBlank = transcriptElementViewportIsBlank(element);
      const currentAnchorOffset = anchorOffset(active, element);
      observeTranscriptReaderExtent(active, snapshot, currentAnchorOffset, viewportBlank);
      correctAnomaly(active, element, snapshot, viewportBlank ? undefined : currentAnchorOffset);
      // After the ordinary 180ms active sampling window, keep the accepted
      // anchor as a passive lease. Mutation/resize/native-scroll observers can
      // still reject a late WebView range swap without spinning a frame loop.
      if (Date.now() >= active.activeFrameDeadline) return;
      active.frame = requestAnimationFrame(tick);
    };
    active.frame = requestAnimationFrame(tick);
  }, [anchorOffset, clearActive, correctAnomaly, generationRef, modeRef, scrollRef]);

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
      expiryTimer: null,
    };
    guardRef.current = active;
    renewLease(active);
    schedule(active);
  }, [anchorOffset, cancel, generationRef, renewLease, schedule, scrollRef]);

  useEffect(() => cancel, [cancel]);

  return useMemo(() => ({ arm, cancel, observe, isActive }), [arm, cancel, observe, isActive]);
}
