import { Loader2, RotateCcw } from "lucide-react";
import {
  forwardRef,
  lazy,
  Suspense,
  useEffect,
  useImperativeHandle,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { estimateTranscriptRowSize, type TranscriptRow } from "../lib/transcriptRows";
import type { TranscriptKernel } from "../lib/transcriptKernel";
import type { TimelineBlock, TimelineProjection, TranscriptRenderMode } from "../lib/transcriptTimeline";
import { useT } from "../lib/i18n";
import { TranscriptSelectionOverlay } from "./TranscriptSelectionOverlay";
import { TranscriptBlockView } from "./TranscriptBlockView";
import { ProcessBrainIcon } from "./ProcessCard";
import { useTick, workStatusLabel } from "../lib/workStatus";

const TranscriptWindow = lazy(() => import("./TranscriptWindow"));

function estimateBlock(block: TimelineBlock): number {
  return Math.max(64, block.rows.reduce((height, row) => height + estimateTranscriptRowSize(row), 0));
}

export type TranscriptViewportHandle = {
  mountBlock: (blockKey: string) => void;
};

type SharedProps = {
  projection: TimelineProjection;
  tabId?: string;
  scrollElement: HTMLDivElement | null;
  renderRow: (row: TranscriptRow) => ReactNode;
  onGeometryWillChange: () => unknown;
  onGeometryChange: () => void;
  onAnomaly: (outcome: "blank-viewport" | "invalid-geometry") => void;
  onGeometryHealthy: () => void;
  running: boolean;
  turnStartAt?: number;
};

function Blocks({ blocks, tabId, renderRow }: {
  blocks: readonly TimelineBlock[];
  tabId?: string;
  renderRow: (row: TranscriptRow) => ReactNode;
}) {
  return <>{blocks.map((block) => (
    <TranscriptBlockView key={block.key} block={block} tabId={tabId} renderRow={renderRow} />
  ))}</>;
}

function ActiveTurnStatus({ turnStartAt }: { turnStartAt?: number }) {
  const t = useT();
  const now = useTick(true);
  const durationMs = turnStartAt ? Math.max(0, now - turnStartAt) : 0;
  return <div className="transcript__live-status" data-kind="reasoning"><ProcessBrainIcon size={12} /><span>{workStatusLabel(durationMs, true, t)}</span></div>;
}

function ResidentTail({ blocks, activeBlock, tabId, renderRow, running, turnStartAt, tailRef }: {
  blocks: readonly TimelineBlock[];
  activeBlock?: TimelineBlock;
  tabId?: string;
  renderRow: (row: TranscriptRow) => ReactNode;
  running: boolean;
  turnStartAt?: number;
  tailRef?: React.RefObject<HTMLDivElement | null>;
}) {
  return (
    <div ref={tailRef} className="transcript__resident-tail" data-transcript-resident-tail="true">
      <Blocks blocks={blocks} tabId={tabId} renderRow={renderRow} />
      {activeBlock && <TranscriptBlockView block={activeBlock} tabId={tabId} renderRow={renderRow} />}
      {running && activeBlock && activeBlock.rows.length <= 1 && <ActiveTurnStatus turnStartAt={turnStartAt} />}
    </div>
  );
}

function FullProjection(props: SharedProps) {
  const { projection, tabId, scrollElement, renderRow, onGeometryWillChange, onGeometryChange, onAnomaly, onGeometryHealthy, running, turnStartAt } = props;
  const rootRef = useRef<HTMLDivElement>(null);

  useLayoutEffect(onGeometryChange, [onGeometryChange, projection]);
  useEffect(() => {
    const element = rootRef.current;
    if (!element || typeof ResizeObserver === "undefined") return;
    let frame: number | null = null;
    const observer = new ResizeObserver(() => {
      if (frame !== null) return;
      onGeometryWillChange();
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
  }, [onGeometryChange, onGeometryWillChange]);
  useEffect(() => {
    if (!scrollElement || scrollElement.clientHeight <= 0) return;
    const frame = requestAnimationFrame(() => {
      if (!Number.isFinite(scrollElement.scrollHeight) || !Number.isFinite(scrollElement.scrollTop)) onAnomaly("invalid-geometry");
      else if (!scrollElement.querySelector("[data-transcript-block-key]")) onAnomaly("blank-viewport");
      else onGeometryHealthy();
    });
    return () => cancelAnimationFrame(frame);
  }, [onAnomaly, onGeometryHealthy, projection, scrollElement]);

  return (
    <div ref={rootRef} className="transcript__projection" data-transcript-render-mode="full" data-transcript-completed-blocks={projection.completedBlocks.length} data-transcript-mounted-blocks={projection.completedBlocks.length + (projection.activeBlock ? 1 : 0)}>
      <ResidentTail blocks={projection.completedBlocks} activeBlock={projection.activeBlock} tabId={tabId} renderRow={renderRow} running={running} turnStartAt={turnStartAt} />
    </div>
  );
}

export const TranscriptViewport = forwardRef<TranscriptViewportHandle, SharedProps & {
  mode: TranscriptRenderMode;
  loadingOlderHistory: boolean;
  olderHistoryError?: string;
  onRetryOlderHistory: () => void;
  kernel: Pick<TranscriptKernel, "anchor" | "userGestureActive">;
  protectedBlockKeys?: ReadonlySet<string>;
}>(function TranscriptViewport({
  projection,
  mode,
  tabId,
  scrollElement,
  renderRow,
  loadingOlderHistory,
  olderHistoryError,
  onRetryOlderHistory,
  onGeometryWillChange,
  onGeometryChange,
  onAnomaly,
  onGeometryHealthy,
  kernel,
  protectedBlockKeys = new Set(),
  running = false,
  turnStartAt,
}, ref) {
  const t = useT();
  const [pinnedJumpBlockKey, setPinnedJumpBlockKey] = useState<string>();
  useImperativeHandle(ref, () => ({ mountBlock: setPinnedJumpBlockKey }), []);
  const prefix = <>
    {projection.hasOlderHistory && (loadingOlderHistory || olderHistoryError) && (
      <div className="transcript__header"><div className="transcript__older-status" role={olderHistoryError ? "alert" : "status"}>
        {loadingOlderHistory
          ? <><Loader2 className="transcript__older-spinner" size={14} aria-hidden="true" /><span>{t("common.loading")}</span></>
          : <><span>{t("transcript.loadEarlierFailed")}</span><button type="button" className="btn btn--small" onClick={onRetryOlderHistory}><RotateCcw size={14} /><span>{t("common.retry")}</span></button></>}
      </div></div>
    )}
  </>;
  const shared = { projection, tabId, scrollElement, renderRow, onGeometryWillChange, onGeometryChange, onAnomaly, onGeometryHealthy, running, turnStartAt };

  if (mode === "full") return <>{prefix}<TranscriptSelectionOverlay tabId={tabId ?? ""} scrollElement={scrollElement} virtualRevision="full" /><FullProjection {...shared} /></>;
  const fallbackBlocks = projection.completedBlocks.slice(-2);
  const activeStatus = running && projection.activeBlock && projection.activeBlock.rows.length <= 1
    ? <ActiveTurnStatus turnStartAt={turnStartAt} />
    : undefined;
  return <Suspense fallback={<>{prefix}<TranscriptSelectionOverlay tabId={tabId ?? ""} scrollElement={scrollElement} virtualRevision="windowed" /><div className="transcript__projection" data-transcript-render-mode="windowed" data-transcript-completed-blocks={projection.completedBlocks.length} data-transcript-mounted-blocks={fallbackBlocks.length + (projection.activeBlock ? 1 : 0)}><ResidentTail blocks={fallbackBlocks} activeBlock={projection.activeBlock} tabId={tabId} renderRow={renderRow} running={running} turnStartAt={turnStartAt} /></div></>}>
    <TranscriptWindow
      {...shared}
      prefix={prefix}
      protectedBlockKeys={protectedBlockKeys}
      kernel={kernel}
      pinnedJumpBlockKey={pinnedJumpBlockKey}
      onPinnedJumpVisible={() => setPinnedJumpBlockKey(undefined)}
      activeStatus={activeStatus}
      estimateBlock={estimateBlock}
      renderBlock={(block) => <TranscriptBlockView key={block.key} block={block} tabId={tabId} renderRow={renderRow} />}
      renderSelectionOverlay={(revision) => <TranscriptSelectionOverlay tabId={tabId ?? ""} scrollElement={scrollElement} virtualRevision={revision} />}
    />
  </Suspense>;
});
