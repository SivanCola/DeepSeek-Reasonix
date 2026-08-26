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

/**
 * Safari/WKWebView and WebKitGTK can defer a native scroll range update by one
 * paint. Chromium applies the same write synchronously; transforming its
 * virtual sizer during a prepend can instead feed a perpetual measurement
 * loop, so the visual bridge is deliberately engine-scoped.
 */
export function shouldBridgeTranscriptReaderCorrection(view: Window): boolean {
  const userAgent = view.navigator.userAgent;
  return /AppleWebKit/i.test(userAgent) && !/(?:Chrome|Chromium|CriOS|Edg|OPR)\//i.test(userAgent);
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
    originalTransform: string;
    view: Window;
  } | null = null;

  const clearReaderVisualBridge = () => {
    const pending = readerVisualBridge;
    readerVisualBridge = null;
    if (!pending) return;
    pending.view.cancelAnimationFrame(pending.frame);
    pending.list.style.transform = pending.originalTransform;
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
          // frame. WebKit can also defer the native range update for one paint,
          // so bridge that frame with a transform that does not affect layout;
          // the next rAF applies the native offset and removes the transform
          // atomically before paint. Virtuoso's native listener observes it.
          clearReaderVisualBridge();
          const targetTop = Math.max(0, Math.min(request.top!, element.scrollHeight - element.clientHeight));
          const correction = targetTop - element.scrollTop;
          const view = element.ownerDocument.defaultView;
          const list = element.querySelector<HTMLElement>(".transcript__virtual-sizer");
          if (view && list && Math.abs(correction) > 2 && shouldBridgeTranscriptReaderCorrection(view)) {
            const originalTransform = list.style.transform;
            list.style.transform = `translateY(${-correction}px)`;
            const pending = {
              frame: 0,
              list,
              originalTransform,
              view,
            };
            pending.frame = view.requestAnimationFrame(() => {
              if (readerVisualBridge !== pending) return;
              readerVisualBridge = null;
              if (
                generationRef.current === generation
                && scrollRef.current === element
                && (modeRef.current === "reader-gesture" || modeRef.current === "manual")
              ) writeNative(element, targetTop, behavior);
              list.style.transform = originalTransform;
            });
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
