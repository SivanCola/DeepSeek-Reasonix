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
import { captureTranscriptLayoutAnchor } from "./transcriptVirtuosoRecovery";

const READER_EXTENT_STABILITY_MS = 180;

type ActiveReaderExtentGuard = TranscriptReaderExtentGuard & {
  element: HTMLDivElement;
  generation: number;
  deadline: number;
  frame: number | null;
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

  const cancel = useCallback(() => {
    const guard = guardRef.current;
    guardRef.current = null;
    if (guard?.frame != null) cancelAnimationFrame(guard.frame);
  }, []);

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
    const snapshot = {
      scrollTop: element.scrollTop,
      scrollHeight: element.scrollHeight,
      clientHeight: element.clientHeight,
    };
    const currentAnchorOffset = anchorOffset(guard, element);
    observeTranscriptReaderExtent(guard, snapshot, currentAnchorOffset);
    correctAnomaly(guard, element, snapshot, currentAnchorOffset);
    return transcriptReaderExtentHasCollapsed(guard);
  }, [anchorOffset, correctAnomaly, scrollRef]);

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
        if (guardRef.current === active) guardRef.current = null;
        return;
      }
      const element = active.element;
      const snapshot = {
        scrollTop: element.scrollTop,
        scrollHeight: element.scrollHeight,
        clientHeight: element.clientHeight,
      };
      const currentAnchorOffset = anchorOffset(active, element);
      observeTranscriptReaderExtent(active, snapshot, currentAnchorOffset);
      correctAnomaly(active, element, snapshot, currentAnchorOffset);
      if (Date.now() >= active.deadline) {
        if (guardRef.current === active) guardRef.current = null;
        return;
      }
      active.frame = requestAnimationFrame(tick);
    };
    active.frame = requestAnimationFrame(tick);
  }, [anchorOffset, correctAnomaly, generationRef, modeRef, scrollRef]);

  const arm = useCallback((deltaY: number) => {
    const element = scrollRef.current;
    if (!element) return;
    const anchor = captureTranscriptLayoutAnchor(element, false);
    const current = guardRef.current;
    if (
      current
      && current.element === element
      && current.generation === generationRef.current
      && extendTranscriptReaderExtentGuard(current, element, anchor, deltaY)
    ) {
      current.deadline = Date.now() + READER_EXTENT_STABILITY_MS;
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
      deadline: Date.now() + READER_EXTENT_STABILITY_MS,
      frame: null,
    };
    guardRef.current = active;
    schedule(active);
  }, [cancel, generationRef, schedule, scrollRef]);

  useEffect(() => cancel, [cancel]);

  return useMemo(() => ({ arm, cancel, observe, isActive }), [arm, cancel, observe, isActive]);
}
