import { defaultRangeExtractor, useVirtualizer } from "@tanstack/react-virtual";
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { TranscriptKernel } from "../lib/transcriptKernel";
import { TranscriptMeasurementLedger } from "../lib/transcriptMeasurementLedger";
import type { TimelineBlock, TimelineProjection } from "../lib/transcriptTimeline";
import { commitTranscriptWindowRange, type TranscriptWindowRange } from "../lib/transcriptWindowRange";

const ANCHOR_MEASUREMENT_RADIUS = 4;
// Keep enough mounted runway for native engines whose scroll event can arrive
// ahead of TanStack's next range calculation. The browser fixtures enforce the
// corresponding 40-block upper bound.
const NATIVE_SCROLL_RUNWAY_BLOCKS = 12;

export default function TranscriptWindow({
  projection,
  scrollElement,
  onGeometryWillChange,
  onGeometryChange,
  onAnomaly,
  onGeometryHealthy,
  protectedBlockKeys,
  kernel,
  pinnedJumpBlockKey,
  onPinnedJumpVisible,
  prefix,
  activeStatus,
  estimateBlock,
  renderBlock,
  renderSelectionOverlay,
}: {
  projection: TimelineProjection;
  scrollElement: HTMLDivElement | null;
  onGeometryWillChange: () => unknown;
  onGeometryChange: () => void;
  onAnomaly: (outcome: "blank-viewport" | "invalid-geometry") => void;
  onGeometryHealthy: () => void;
  protectedBlockKeys: ReadonlySet<string>;
  kernel: Pick<TranscriptKernel, "anchor" | "userGestureActive">;
  pinnedJumpBlockKey?: string;
  onPinnedJumpVisible: () => void;
  prefix: ReactNode;
  activeStatus?: ReactNode;
  estimateBlock: (block: TimelineBlock) => number;
  renderBlock: (block: TimelineBlock) => ReactNode;
  renderSelectionOverlay: (revision: string) => ReactNode;
}) {
  const minimumResidentIndex = Math.max(0, projection.completedBlocks.length - 2);
  const minimumResidentKey = projection.completedBlocks[minimumResidentIndex]?.key;
  const [residentStartKey, setResidentStartKey] = useState<string | undefined>(minimumResidentKey);
  const currentResidentIndex = residentStartKey
    ? projection.completedBlocks.findIndex((block) => block.key === residentStartKey)
    : -1;
  const residentStartIndex = currentResidentIndex >= 0 ? Math.min(currentResidentIndex, minimumResidentIndex) : minimumResidentIndex;
  const split = useMemo(() => ({
    cold: projection.completedBlocks.slice(0, residentStartIndex),
    resident: projection.completedBlocks.slice(residentStartIndex),
  }), [projection.completedBlocks, residentStartIndex]);
  const coldIndexByKey = useMemo(() => new Map(split.cold.map((block, index) => [block.key, index])), [split.cold]);
  const retainedIndexes = new Set<number>();
  const retainKey = (key: string | undefined, radius = 0) => {
    const index = key ? coldIndexByKey.get(key) : undefined;
    if (index == null) return;
    for (let candidate = Math.max(0, index - radius); candidate <= Math.min(split.cold.length - 1, index + radius); candidate += 1) retainedIndexes.add(candidate);
  };
  protectedBlockKeys.forEach((key) => retainKey(key));
  const focusedBlock = document.activeElement instanceof Element
    ? document.activeElement.closest<HTMLElement>("[data-transcript-block-key]")?.dataset.transcriptBlockKey
    : undefined;
  retainKey(focusedBlock);
  retainKey(kernel.anchor.kind === "block" ? kernel.anchor.blockKey : undefined, ANCHOR_MEASUREMENT_RADIUS);
  retainKey(pinnedJumpBlockKey, ANCHOR_MEASUREMENT_RADIUS);
  const coldContainerRef = useRef<HTMLDivElement>(null);
  const residentTailRef = useRef<HTMLDivElement>(null);
  const measurementLedgerRef = useRef<TranscriptMeasurementLedger | null>(null);
  if (!measurementLedgerRef.current) measurementLedgerRef.current = new TranscriptMeasurementLedger();
  const measurementLedger = measurementLedgerRef.current;
  const scrollMargin = coldContainerRef.current?.offsetTop ?? 0;
  const virtualizer = useVirtualizer({
    count: split.cold.length,
    getScrollElement: () => scrollElement,
    estimateSize: (index) => {
      const block = split.cold[index];
      return measurementLedger.sizeFor(block.key, estimateBlock(block));
    },
    getItemKey: (index) => split.cold[index].key,
    overscan: NATIVE_SCROLL_RUNWAY_BLOCKS,
    // The window adapter owns the DOM-to-ledger commit below. TanStack still
    // observes stable item identities, but cannot publish ResizeObserver
    // measurements independently of the kernel's native-gesture boundary.
    useCachedMeasurements: true,
    rangeExtractor: (range) => {
      const indexes = new Set(defaultRangeExtractor(range));
      retainedIndexes.forEach((index) => indexes.add(index));
      return Array.from(indexes).sort((left, right) => left - right);
    },
    scrollMargin,
    scrollToFn: () => {},
  });
  virtualizer.shouldAdjustScrollPositionOnItemSizeChange = () => false;
  // Materialize TanStack's prefix-size ledger before reading either its
  // asynchronous candidate range or the synchronous recovery input.
  const totalSize = virtualizer.getTotalSize();
  const candidateItems = virtualizer.getVirtualItems();
  const committedRangeRef = useRef<TranscriptWindowRange<(typeof candidateItems)[number]> | undefined>(undefined);
  const structureRevision = `${split.cold.length}:${split.cold[0]?.key ?? ""}:${split.cold[split.cold.length - 1]?.key ?? ""}`;
  const committedRange = commitTranscriptWindowRange({
    candidate: candidateItems,
    measurements: virtualizer.measurementsCache,
    retainedIndexes,
    previous: committedRangeRef.current,
    structureRevision,
    scrollTop: scrollElement?.scrollTop ?? 0,
    clientHeight: scrollElement?.clientHeight ?? 0,
    coldStart: scrollMargin,
    coldEnd: scrollMargin + totalSize,
    overscan: NATIVE_SCROLL_RUNWAY_BLOCKS,
    gestureActive: kernel.userGestureActive,
  });
  const virtualItems = committedRange.items;
  const rangeRevision = virtualItems.map((item) => `${String(item.key)}:${item.start}:${item.size}`).join("|");

  useLayoutEffect(() => {
    committedRangeRef.current = committedRange;
  }, [committedRange]);
  useLayoutEffect(() => {
    if (!minimumResidentKey || currentResidentIndex >= 0) return;
    setResidentStartKey(minimumResidentKey);
  }, [currentResidentIndex, minimumResidentKey]);
  useLayoutEffect(() => {
    const validKeys = new Set(projection.completedBlocks.map((block) => block.key));
    measurementLedger.retain(validKeys);
  }, [measurementLedger, projection.completedBlocks]);
  useLayoutEffect(() => {
    if (residentStartIndex >= minimumResidentIndex || !scrollElement) return;
    const block = projection.completedBlocks[residentStartIndex];
    const element = block
      ? Array.from(scrollElement.querySelectorAll<HTMLElement>("[data-transcript-block-key]"))
        .find((candidate) => candidate.dataset.transcriptBlockKey === block.key)
      : undefined;
    const viewport = scrollElement.getBoundingClientRect();
    if (!element || element.contains(document.activeElement) || protectedBlockKeys.has(block.key) || element.getBoundingClientRect().bottom >= viewport.top - scrollElement.clientHeight) return;
    setResidentStartKey(projection.completedBlocks[residentStartIndex + 1]?.key ?? minimumResidentKey);
  }, [minimumResidentIndex, minimumResidentKey, projection.completedBlocks, protectedBlockKeys, residentStartIndex, scrollElement]);
  useEffect(() => {
    if (!pinnedJumpBlockKey || !scrollElement) return;
    const target = Array.from(scrollElement.querySelectorAll<HTMLElement>("[data-transcript-block-key]"))
      .find((element) => element.dataset.transcriptBlockKey === pinnedJumpBlockKey);
    if (!target) return;
    const viewport = scrollElement.getBoundingClientRect();
    const rect = target.getBoundingClientRect();
    if (rect.bottom >= viewport.top && rect.top <= viewport.bottom) onPinnedJumpVisible();
  }, [onPinnedJumpVisible, pinnedJumpBlockKey, rangeRevision, scrollElement]);
  useLayoutEffect(() => {
    if (kernel.userGestureActive) return;
    const container = coldContainerRef.current;
    const changes: Array<{ key: string; size: number }> = [];
    if (container) {
      for (const item of virtualItems) {
        const element = container.querySelector<HTMLElement>(`.transcript__window-item[data-index="${item.index}"]`);
        if (!element) continue;
        const size = Math.max(64, element.getBoundingClientRect().height || element.offsetHeight);
        if (Math.abs(size - item.size) > 0.5) changes.push({ key: String(item.key), size });
      }
    }
    if (changes.length > 0 && measurementLedger.commit(changes)) {
      onGeometryWillChange();
      // `measure()` invalidates TanStack exactly once. Its estimate callback
      // reads the complete immutable ledger snapshot on the next render, so
      // no partially-updated prefix tree can reach the native viewport.
      virtualizer.measure();
      return;
    }
    onGeometryChange();
  }, [kernel.userGestureActive, measurementLedger, onGeometryChange, onGeometryWillChange, projection.activeBlock?.measurementRevision, rangeRevision, split.resident, virtualItems, virtualizer]);
  useEffect(() => {
    const element = residentTailRef.current;
    if (!element || typeof ResizeObserver === "undefined") return;
    let frame: number | null = null;
    const observer = new ResizeObserver(() => {
      if (frame !== null) return;
      frame = requestAnimationFrame(() => {
        frame = null;
        onGeometryChange();
      });
    });
    observer.observe(element);
    return () => {
      observer.disconnect();
      if (frame !== null) cancelAnimationFrame(frame);
    };
  }, [onGeometryChange]);
  useEffect(() => {
    if (!scrollElement || scrollElement.clientHeight <= 0) return;
    const frame = requestAnimationFrame(() => {
      if (!Number.isFinite(scrollElement.scrollHeight) || !Number.isFinite(scrollElement.scrollTop)) {
        onAnomaly("invalid-geometry");
        return;
      }
      const viewport = scrollElement.getBoundingClientRect();
      // A detached/hidden surface has no paintable native viewport yet, so it
      // cannot provide evidence of a blank committed range.
      if (viewport.height <= 0) return;
      const visible = Array.from(scrollElement.querySelectorAll<HTMLElement>("[data-transcript-block-key]"))
        .some((element) => {
          const rect = element.getBoundingClientRect();
          return rect.height > 0 && rect.bottom >= viewport.top && rect.top <= viewport.bottom;
        });
      if (!visible && (projection.completedBlocks.length > 0 || projection.activeBlock)) onAnomaly("blank-viewport");
      else onGeometryHealthy();
    });
    return () => cancelAnimationFrame(frame);
  }, [onAnomaly, onGeometryHealthy, projection.activeBlock, projection.completedBlocks.length, rangeRevision, scrollElement]);

  return (
    <div className="transcript__projection" data-transcript-render-mode="windowed" data-transcript-range-source={committedRange.source} data-transcript-completed-blocks={projection.completedBlocks.length} data-transcript-mounted-blocks={virtualItems.length + split.resident.length + (projection.activeBlock ? 1 : 0)}>
      {prefix}
      {renderSelectionOverlay(`windowed:${rangeRevision}`)}
      {split.cold.length > 0 && (
        <div ref={coldContainerRef} className="transcript__window" style={{ height: totalSize, position: "relative" }}>
          {virtualItems.map((virtualItem) => {
            const block = split.cold[virtualItem.index];
            return <div key={block.key} data-index={virtualItem.index} className="transcript__window-item" style={{ position: "absolute", top: 0, left: 0, width: "100%", transform: `translateY(${virtualItem.start - scrollMargin}px)` }}>
              {renderBlock(block)}
            </div>;
          })}
        </div>
      )}
      <div ref={residentTailRef} className="transcript__resident-tail" data-transcript-resident-tail="true">
        {split.resident.map(renderBlock)}
        {projection.activeBlock && renderBlock(projection.activeBlock)}
        {activeStatus}
      </div>
    </div>
  );
}
