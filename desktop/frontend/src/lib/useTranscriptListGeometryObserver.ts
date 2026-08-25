import { useEffect } from "react";
import type { TranscriptGeometryChangeSource } from "./transcriptGeometryRevision";

/**
 * Observe the rendered list box as a lossless producer for real row-size
 * changes that WebViews do not always report through itemSize. These signals
 * use the deferred row-measure path: aggregate totalListHeightChanged remains
 * diagnostics-only and a tail write cannot synchronously feed itself back.
 */
export function useTranscriptListGeometryObserver({
  scrollElement,
  enabled,
  surfaceKey,
  noteGeometryChange,
}: {
  scrollElement: HTMLDivElement | null;
  enabled: boolean;
  surfaceKey: string;
  noteGeometryChange: (source: TranscriptGeometryChangeSource) => void;
}) {
  useEffect(() => {
    if (!enabled || !scrollElement || typeof ResizeObserver === "undefined") return;
    const MutationObserverCtor = scrollElement.ownerDocument.defaultView?.MutationObserver;
    if (!MutationObserverCtor) return;
    let observedList: HTMLElement | null = null;
    let previousHeight = 0;
    const observer = new ResizeObserver(() => {
      if (!observedList) return;
      const height = observedList.getBoundingClientRect().height;
      if (Math.abs(height - previousHeight) <= 0.5) return;
      previousHeight = height;
      noteGeometryChange("row-measure");
    });
    const attachCurrentList = () => {
      if (observedList?.isConnected && scrollElement.contains(observedList)) return;
      observer.disconnect();
      observedList = scrollElement.querySelector<HTMLElement>(".transcript__virtual-sizer");
      if (!observedList) return;
      previousHeight = observedList.getBoundingClientRect().height;
      observer.observe(observedList);
    };
    // Virtuoso can mount or replace its sizer after the scroller ref and this
    // effect have committed. Track that lifecycle without turning every row
    // mutation into a geometry revision; once attached, the fast connected
    // check exits before querying the subtree.
    const mountObserver = new MutationObserverCtor(attachCurrentList);
    mountObserver.observe(scrollElement, { childList: true, subtree: true });
    attachCurrentList();
    return () => {
      mountObserver.disconnect();
      observer.disconnect();
    };
  }, [enabled, noteGeometryChange, scrollElement, surfaceKey]);
}
