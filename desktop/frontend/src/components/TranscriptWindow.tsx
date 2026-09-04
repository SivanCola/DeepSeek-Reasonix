import { defaultRangeExtractor, useVirtualizer } from "@tanstack/react-virtual";
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { TimelineBlock, TimelineProjection } from "../lib/transcriptTimeline";

const ANCHOR_MEASUREMENT_RADIUS = 4;

export default function TranscriptWindow({
  projection,
  scrollElement,
  onGeometryChange,
  onAnomaly,
  protectedBlockKeys,
  anchorBlockKey,
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
  onGeometryChange: () => void;
  onAnomaly: (outcome: "blank-viewport" | "invalid-geometry") => void;
  protectedBlockKeys: ReadonlySet<string>;
  anchorBlockKey?: string;
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
  const coldContainerRef = useRef<HTMLDivElement>(null);
  const residentTailRef = useRef<HTMLDivElement>(null);
  const scrollMargin = coldContainerRef.current?.offsetTop ?? 0;
  const virtualizer = useVirtualizer({
    count: split.cold.length,
    getScrollElement: () => scrollElement,
    estimateSize: (index) => estimateBlock(split.cold[index]),
    getItemKey: (index) => split.cold[index]?.key ?? index,
    overscan: 4,
    measureElement: (element) => Math.max(64, element.getBoundingClientRect().height || (element as HTMLElement).offsetHeight),
    rangeExtractor: (range) => {
      const indexes = new Set(defaultRangeExtractor(range));
      const addKey = (key: string | undefined, radius = 0) => {
        const index = key ? coldIndexByKey.get(key) : undefined;
        if (index == null) return;
        for (let candidate = Math.max(0, index - radius); candidate <= Math.min(split.cold.length - 1, index + radius); candidate += 1) indexes.add(candidate);
      };
      protectedBlockKeys.forEach((key) => addKey(key));
      const focusedBlock = document.activeElement instanceof Element
        ? document.activeElement.closest<HTMLElement>("[data-transcript-block-key]")?.dataset.transcriptBlockKey
        : undefined;
      addKey(focusedBlock);
      addKey(anchorBlockKey, ANCHOR_MEASUREMENT_RADIUS);
      addKey(pinnedJumpBlockKey, ANCHOR_MEASUREMENT_RADIUS);
      return Array.from(indexes).sort((left, right) => left - right);
    },
    scrollMargin,
    scrollToFn: () => {},
  });
  virtualizer.shouldAdjustScrollPositionOnItemSizeChange = () => false;
  const virtualItems = virtualizer.getVirtualItems();
  const rangeRevision = virtualItems.map((item) => `${String(item.key)}:${item.start}:${item.size}`).join("|");

  useLayoutEffect(() => {
    if (!minimumResidentKey || currentResidentIndex >= 0) return;
    setResidentStartKey(minimumResidentKey);
  }, [currentResidentIndex, minimumResidentKey]);
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
  useLayoutEffect(onGeometryChange, [onGeometryChange, projection.activeBlock?.measurementRevision, rangeRevision, split.resident]);
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
      const visible = Array.from(scrollElement.querySelectorAll<HTMLElement>("[data-transcript-block-key]"))
        .some((element) => {
          const rect = element.getBoundingClientRect();
          return rect.height > 0 && rect.bottom >= viewport.top && rect.top <= viewport.bottom;
        });
      if (!visible && (projection.completedBlocks.length > 0 || projection.activeBlock)) onAnomaly("blank-viewport");
    });
    return () => cancelAnimationFrame(frame);
  }, [onAnomaly, projection.activeBlock, projection.completedBlocks.length, rangeRevision, scrollElement]);

  return (
    <div className="transcript__projection" data-transcript-render-mode="windowed" data-transcript-completed-blocks={projection.completedBlocks.length} data-transcript-mounted-blocks={virtualItems.length + split.resident.length + (projection.activeBlock ? 1 : 0)}>
      {prefix}
      {renderSelectionOverlay(`windowed:${rangeRevision}`)}
      {split.cold.length > 0 && (
        <div ref={coldContainerRef} className="transcript__window" style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
          {virtualItems.map((virtualItem) => {
            const block = split.cold[virtualItem.index];
            return <div key={block.key} ref={virtualizer.measureElement} data-index={virtualItem.index} className="transcript__window-item" style={{ position: "absolute", top: 0, left: 0, width: "100%", transform: `translateY(${virtualItem.start - scrollMargin}px)` }}>
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
