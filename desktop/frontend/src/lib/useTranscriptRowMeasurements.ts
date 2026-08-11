import { useCallback, useEffect, useMemo, useRef, type RefObject } from "react";
import type { Virtualizer } from "@tanstack/react-virtual";
import { estimateTranscriptRowSize, type TranscriptRow } from "./transcriptRows";
import type { TranscriptScrollOwner } from "./transcriptScrollController";
import {
  createTranscriptMeasureElement,
  EMPTY_TRANSCRIPT_LAYOUT_SNAPSHOT,
  estimateTranscriptRowHeightForLayout,
} from "./transcriptHeightCache";

export function useTranscriptRowMeasurements(
  tabId: string | undefined,
  rows: readonly TranscriptRow[],
  {
    gestureUntilRef,
    stick,
    scheduleRepinIfWasPinned,
    scrollToBottomAfterLayout,
  }: {
    gestureUntilRef: RefObject<number>;
    stick: RefObject<boolean>;
    scheduleRepinIfWasPinned: (containerHeightDelta: number, owner?: TranscriptScrollOwner) => void;
    scrollToBottomAfterLayout: (frames?: number, owner?: TranscriptScrollOwner) => void;
  },
) {
  const layoutSnapshotRef = useRef(EMPTY_TRANSCRIPT_LAYOUT_SNAPSHOT);
  const getItemKey = useCallback((index: number) => `${tabId ?? ""}:${String(rows[index]?.key ?? index)}`, [rows, tabId]);
  const estimateSize = useCallback((index: number) => {
    const row = rows[index];
    return estimateTranscriptRowHeightForLayout({
      tabId: tabId ?? "",
      layout: layoutSnapshotRef.current,
      rowKey: String(row?.key ?? index),
      row,
      fallback: estimateTranscriptRowSize(row),
    });
  }, [rows, tabId]);
  const measureRowSize = useMemo(() => createTranscriptMeasureElement({
    tabId: tabId ?? "",
    getLayoutSnapshot: () => layoutSnapshotRef.current,
  }), [tabId]);
  const deferredRowMeasurementsRef = useRef(new Map<string, number>());
  const onMeasurementsFlushed = useCallback(() => {
    // TanStack can publish its corrected total size one frame after measure().
    if (stick.current) scrollToBottomAfterLayout(2, "row-size");
  }, [scrollToBottomAfterLayout, stick]);
  const measureElement = useCallback(
    (element: HTMLDivElement, entry: ResizeObserverEntry | undefined, instance: Virtualizer<HTMLDivElement, HTMLDivElement>) => {
      if (gestureUntilRef.current > Date.now()) {
        const index = instance.indexFromElement(element);
        const key = index >= 0 && index < rows.length ? instance.options.getItemKey(index) : null;
        const box = entry?.borderBoxSize?.[0];
        const measured = box ? Math.round(box.blockSize) : element.offsetHeight;
        const frozen = key == null ? undefined : instance.itemSizeCache.get(key) ?? instance.measurementsCache[index]?.size;
        const rowKey = element.dataset.rowKey;
        if (rowKey && measured > 0 && frozen !== measured) deferredRowMeasurementsRef.current.set(rowKey, measured);
        if (stick.current) scheduleRepinIfWasPinned(0, "row-size");
        return frozen ?? measured;
      }
      const height = measureRowSize(element, entry, instance);
      if (stick.current) scheduleRepinIfWasPinned(0, "row-size");
      return height;
    },
    [gestureUntilRef, measureRowSize, rows.length, scheduleRepinIfWasPinned, stick],
  );
  useEffect(() => () => deferredRowMeasurementsRef.current.clear(), [tabId]);
  return { getItemKey, estimateSize, layoutSnapshotRef, measureElement, deferredRowMeasurementsRef, onMeasurementsFlushed };
}
