import type { RefObject } from "react";
import type { VirtuosoHandle } from "react-virtuoso";
import type { TranscriptScrollMode } from "./transcriptScrollArbiter";
import { noteTranscriptScrollWrite, type TranscriptScrollWriteRecord } from "./transcriptScrollProbe";

export type TranscriptScrollWriterRequest = Omit<
  TranscriptScrollWriteRecord,
  "kind" | "source" | "generation" | "geometryRevision"
> & {
  operation: "scrollTo" | "scrollBy" | "scrollToIndex";
  source: string;
  behavior?: ScrollBehavior;
  align?: "start" | "center" | "end";
  expectedGeneration: number;
  geometryRevision: number;
};

export type TranscriptScrollWriter = {
  write: (request: TranscriptScrollWriterRequest) => boolean;
  lastOwner: () => string | undefined;
};

const READER_BRIDGE_MAX_FRAMES = 6;

function captureReaderBridgeRows(element: HTMLDivElement, list: HTMLElement): ReadonlyMap<string, number> {
  const viewport = element.getBoundingClientRect();
  const rows = new Map<string, number>();
  for (const row of list.querySelectorAll<HTMLElement>(".transcript__row[data-row-key]")) {
    const rowKey = row.dataset.rowKey;
    const rect = row.getBoundingClientRect();
    if (rowKey && rect.bottom > viewport.top && rect.top < viewport.bottom) {
      rows.set(rowKey, rect.top - viewport.top);
    }
  }
  return rows;
}

/**
 * Safari/WKWebView and WebKitGTK can defer a native scroll range update by one
 * paint. WebView2 applies the offset synchronously but can replace it with the
 * same frame's virtual range. Ordinary Chromium does neither, so the visual
 * bridge remains scoped to the affected desktop engines.
 */
export function shouldBridgeTranscriptReaderCorrection(view: Window): boolean {
  const userAgent = view.navigator.userAgent;
  return /Edg\//i.test(userAgent)
    || (/AppleWebKit/i.test(userAgent) && !/(?:Chrome|Chromium|CriOS|OPR)\//i.test(userAgent));
}

/**
 * The only production gateway allowed to call the transcript Virtuoso handle.
 * Async controllers attach the generation they captured; stale writes are
 * rejected before they can land on a replacement surface. Native scrollbar
 * dragging is browser-owned and suppresses every imperative write.
 */
export function createTranscriptScrollWriter({
  virtuosoRef,
  scrollRef,
  modeRef,
  generationRef,
}: {
  virtuosoRef: RefObject<VirtuosoHandle | null>;
  scrollRef: RefObject<HTMLDivElement | null>;
  modeRef: RefObject<TranscriptScrollMode>;
  generationRef: RefObject<number>;
}): TranscriptScrollWriter {
  let sequence = 0;
  let lastOwner: string | undefined;
  let readerVisualBridge: {
    frame: number;
    list: HTMLElement;
    originalTop: string;
    view: Window;
    attempts: number;
    cleanupTimer: number | null;
    observer: MutationObserver | null;
    rowOffsets: ReadonlyMap<string, number>;
    listTransform: string;
    offsetY: number;
  } | null = null;

  const clearReaderVisualBridge = () => {
    const pending = readerVisualBridge;
    readerVisualBridge = null;
    if (!pending) return;
    if (pending.frame > 0) pending.view.cancelAnimationFrame(pending.frame);
    if (pending.cleanupTimer != null) pending.view.clearTimeout(pending.cleanupTimer);
    pending.observer?.disconnect();
    if (pending.originalTop) pending.list.style.top = pending.originalTop;
    else pending.list.style.removeProperty("top");
  };

  const writeNative = (element: HTMLDivElement, top: number, behavior: ScrollBehavior) => {
    if (typeof element.scrollTo === "function") element.scrollTo({ top, behavior });
    else element.scrollTop = top;
  };

  const write = (request: TranscriptScrollWriterRequest): boolean => {
    const handle = virtuosoRef.current;
    const element = scrollRef.current;
    const generation = generationRef.current;
    if (!handle || !element || modeRef.current === "native-thumb") return false;
    if (request.expectedGeneration !== generation) return false;
    if (request.operation === "scrollToIndex" ? request.index === undefined : request.top === undefined) return false;

    sequence += 1;
    lastOwner = request.owner;
    const record: TranscriptScrollWriteRecord = {
      owner: request.owner,
      kind: request.operation,
      top: request.top,
      index: request.index,
      source: request.source,
      phase: request.phase,
      scrollTop: element.scrollTop,
      scrollHeight: element.scrollHeight,
      clientHeight: element.clientHeight,
      bottomDistance: element.scrollHeight - element.scrollTop - element.clientHeight,
      mode: modeRef.current,
      sequence,
      generation,
      geometryRevision: request.geometryRevision,
      settleFrame: request.settleFrame,
      offBottomFrames: request.offBottomFrames,
      stagnantFrames: request.stagnantFrames,
    };
    noteTranscriptScrollWrite(record);

    const behavior = request.behavior === "smooth" ? "smooth" : "auto";
    switch (request.operation) {
      case "scrollTo":
        if (request.owner === "reader-stability") {
          // Reader protection corrects the currently painted native range.
          // Sending the same command through Virtuoso can enqueue a second
          // range reconciliation and reintroduce the displacement on the next
          // frame. WebKit can defer the native range update, so retain a
          // layout-neutral bridge until the native offset acknowledges the
          // target (with a bounded retry budget).
          clearReaderVisualBridge();
          const targetTop = Math.max(0, Math.min(request.top!, element.scrollHeight - element.clientHeight));
          const correction = targetTop - element.scrollTop;
          const view = element.ownerDocument.defaultView;
          const list = element.querySelector<HTMLElement>(".transcript__virtual-sizer");
          if (view && list && Math.abs(correction) > 2 && shouldBridgeTranscriptReaderCorrection(view)) {
            // Virtuoso owns `transform`; relative `top` is an independent,
            // layout-neutral visual offset supported by older WKWebView builds
            // that do not reliably paint the individual `translate` property.
            const originalTop = list.style.top;
            const paintedRows = captureReaderBridgeRows(element, list);
            const pending = {
              frame: 0,
              list,
              originalTop,
              view,
              attempts: 0,
              cleanupTimer: null as number | null,
              observer: null as MutationObserver | null,
              rowOffsets: new Map([...paintedRows].map(([rowKey, offset]) => [rowKey, offset - correction])),
              listTransform: list.style.transform,
              offsetY: -correction,
            };
            const setVisualOffset = (offsetY: number) => {
              pending.offsetY = offsetY;
              list.style.top = `${offsetY}px`;
            };
            setVisualOffset(-correction);
            const MutationObserverCtor = view.MutationObserver;
            if (MutationObserverCtor && pending.rowOffsets.size > 0) {
              pending.observer = new MutationObserverCtor((mutations) => {
                if (readerVisualBridge !== pending) return;
                const rangeChanged = mutations.some((mutation) => {
                  if (mutation.type === "childList") return true;
                  if (mutation.target !== list) return true;
                  const listTransform = list.style.transform;
                  if (listTransform === pending.listTransform) return false;
                  pending.listTransform = listTransform;
                  return true;
                });
                // `setVisualOffset` itself mutates the list's style attribute.
                // Ignore that independent-property-only record, while still
                // observing Virtuoso's transform changes on the same node.
                if (!rangeChanged) return;
                const currentRows = captureReaderBridgeRows(element, list);
                const deltas = [...currentRows].flatMap(([rowKey, offset]) => {
                  const target = pending.rowOffsets.get(rowKey);
                  return target === undefined ? [] : [offset - target];
                }).sort((left, right) => left - right);
                if (deltas.length === 0) return;
                const displacement = deltas[Math.floor(deltas.length / 2)];
                if (Math.abs(displacement) <= 2) return;
                // A native acknowledgement can be followed by Virtuoso's
                // queued range replacement later in the same paint. Hold the
                // last corrected rows in place until the reader guard issues
                // the replacement range's final native target.
                setVisualOffset(pending.offsetY - displacement);
              });
              pending.observer.observe(list, {
                childList: true,
                subtree: true,
                attributes: true,
                attributeFilter: ["style"],
              });
            }
            const commit = () => {
              if (readerVisualBridge !== pending) return;
              pending.frame = 0;
              if (!(
                generationRef.current === generation
                && scrollRef.current === element
                && (modeRef.current === "reader-gesture" || modeRef.current === "manual")
              )) {
                clearReaderVisualBridge();
                return;
              }
              pending.attempts += 1;
              writeNative(element, targetTop, behavior);
              const remaining = element.scrollTop - targetTop;
              if (Math.abs(remaining) <= 2) {
                setVisualOffset(0);
                // Do not tear down in the acknowledgement rAF. WKWebView can
                // apply its queued Virtuoso range after this callback but
                // before the same rendering opportunity paints.
                pending.frame = view.requestAnimationFrame(() => {
                  pending.frame = 0;
                  pending.cleanupTimer = view.setTimeout(() => {
                    pending.cleanupTimer = null;
                    if (readerVisualBridge === pending) clearReaderVisualBridge();
                  }, 0);
                });
                return;
              }
              if (pending.attempts >= READER_BRIDGE_MAX_FRAMES) {
                clearReaderVisualBridge();
                return;
              }
              setVisualOffset(remaining);
              pending.frame = view.requestAnimationFrame(commit);
            };
            pending.frame = view.requestAnimationFrame(commit);
            readerVisualBridge = pending;
            return true;
          }
          writeNative(element, targetTop, behavior);
          return true;
        }
        clearReaderVisualBridge();
        // Keep Virtuoso's internal location and the current native scroller
        // synchronized as one logical write. The handle can briefly point at
        // a superseded scroller during a surface commit, while a native-only
        // write can be overwritten by Virtuoso's next mount frame. Issuing
        // the same target through both paths inside this gateway covers both
        // races without creating a second owner or diagnostic sequence.
        handle.scrollTo({ top: request.top, behavior });
        if (typeof element.scrollTo === "function") writeNative(element, request.top!, behavior);
        return true;
      case "scrollBy":
        clearReaderVisualBridge();
        handle.scrollBy({ top: request.top, behavior });
        return true;
      case "scrollToIndex":
        clearReaderVisualBridge();
        handle.scrollToIndex({ index: request.index!, align: request.align ?? "start", behavior });
        return true;
    }
  };

  return { write, lastOwner: () => lastOwner };
}
