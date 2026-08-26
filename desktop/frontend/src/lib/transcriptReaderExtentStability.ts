import type { TranscriptLayoutAnchor } from "./transcriptVirtuosoRecovery";
import type { TranscriptScrollEvent } from "./transcriptScrollArbiter";

export const MIN_REVERSE_JUMP_PX = 96;
const REVERSE_JUMP_VIEWPORT_RATIO = 0.5;
const EXTENT_REBOUND_VIEWPORT_RATIO = 0.5;
const DIRECTION_JITTER_PX = 2;

export type TranscriptExtentSnapshot = {
  scrollTop: number;
  scrollHeight: number;
  clientHeight: number;
};

export type TranscriptReaderExtentGuard = {
  direction: -1 | 1;
  baselineTop: number;
  baselineHeight: number;
  acceptedTop: number;
  acceptedHeight: number;
  minimumHeight: number;
  clientHeight: number;
  expectedTop: number;
  collapsed: boolean;
  anomalyReported: boolean;
  anchor?: Extract<TranscriptLayoutAnchor, { mode: "manual" }>;
  anchorScrollTop?: number;
  targetAnchorOffset?: number;
  blankTop?: number;
};

function clamp(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, value));
}

function directionFor(deltaY: number): -1 | 1 {
  return deltaY < 0 ? -1 : 1;
}

function manualAnchor(anchor: TranscriptLayoutAnchor | undefined) {
  return anchor?.mode === "manual" ? anchor : undefined;
}

export function createTranscriptReaderExtentGuard(
  snapshot: TranscriptExtentSnapshot,
  anchor: TranscriptLayoutAnchor | undefined,
  deltaY: number,
): TranscriptReaderExtentGuard | undefined {
  if (!Number.isFinite(deltaY) || deltaY === 0 || snapshot.clientHeight <= 0) return undefined;
  const maxTop = Math.max(0, snapshot.scrollHeight - snapshot.clientHeight);
  const acceptedAnchor = manualAnchor(anchor);
  return {
    direction: directionFor(deltaY),
    baselineTop: snapshot.scrollTop,
    baselineHeight: snapshot.scrollHeight,
    acceptedTop: snapshot.scrollTop,
    acceptedHeight: snapshot.scrollHeight,
    minimumHeight: snapshot.scrollHeight,
    clientHeight: snapshot.clientHeight,
    expectedTop: clamp(snapshot.scrollTop + deltaY, 0, maxTop),
    collapsed: false,
    anomalyReported: false,
    anchor: acceptedAnchor,
    anchorScrollTop: acceptedAnchor ? snapshot.scrollTop : undefined,
    targetAnchorOffset: acceptedAnchor ? acceptedAnchor.offset - deltaY : undefined,
  };
}

/** Extend one continuous same-direction gesture without discarding its last
 * accepted logical position. Direction changes create a fresh transaction. */
export function extendTranscriptReaderExtentGuard(
  guard: TranscriptReaderExtentGuard,
  snapshot: TranscriptExtentSnapshot,
  anchor: TranscriptLayoutAnchor | undefined,
  deltaY: number,
): boolean {
  if (directionFor(deltaY) !== guard.direction || Math.abs(snapshot.clientHeight - guard.clientHeight) > 1) return false;
  const maxTop = Math.max(0, Math.max(snapshot.scrollHeight, guard.acceptedHeight) - snapshot.clientHeight);
  const acceptedAnchor = manualAnchor(anchor);
  guard.expectedTop = clamp(guard.expectedTop + deltaY, 0, maxTop);
  const movement = snapshot.scrollTop - guard.acceptedTop;
  if (!guard.collapsed && guard.direction * movement >= -DIRECTION_JITTER_PX && acceptedAnchor) {
    guard.anchor = acceptedAnchor;
    guard.anchorScrollTop = snapshot.scrollTop;
    guard.targetAnchorOffset = acceptedAnchor.offset - deltaY;
  }
  return true;
}

export function observeTranscriptReaderExtent(
  guard: TranscriptReaderExtentGuard,
  snapshot: TranscriptExtentSnapshot,
  currentAnchorOffset?: number,
  viewportBlank = false,
): void {
  if (Math.abs(snapshot.clientHeight - guard.clientHeight) > 1) return;
  guard.minimumHeight = Math.min(guard.minimumHeight, snapshot.scrollHeight);
  if (transcriptReaderExtentHasCollapsed(guard)) {
    guard.collapsed = true;
    return;
  }

  // A native scroller can outrun Virtuoso's mounted range for one or two
  // frames. That empty coordinate is not a logical reader position: remember
  // its furthest directional offset, but never let it replace the last
  // accepted row. When the new range mounts, a reverse from this watermark is
  // corrected before it can become the new baseline.
  if (viewportBlank) {
    guard.blankTop = guard.blankTop === undefined
      ? snapshot.scrollTop
      : guard.direction > 0
        ? Math.max(guard.blankTop, snapshot.scrollTop)
        : Math.min(guard.blankTop, snapshot.scrollTop);
    return;
  }
  const blankReverse = guard.blankTop === undefined
    ? 0
    : guard.direction * (guard.blankTop - snapshot.scrollTop);
  if (blankReverse >= MIN_REVERSE_JUMP_PX) return;
  guard.blankTop = undefined;

  // Do not replace the last accepted logical position with a same-direction
  // native offset whose painted anchor moved backwards. Virtuoso can advance
  // scrollTop while swapping in an older range, so scrollTop direction alone
  // is not sufficient evidence that the reader advanced.
  if (transcriptReaderAnchorReverseDelta(guard, snapshot, currentAnchorOffset) >= MIN_REVERSE_JUMP_PX) return;
  const movement = snapshot.scrollTop - guard.acceptedTop;
  if (guard.direction * movement >= -DIRECTION_JITTER_PX) {
    guard.acceptedTop = snapshot.scrollTop;
    guard.acceptedHeight = snapshot.scrollHeight;
    guard.baselineTop = snapshot.scrollTop;
    guard.baselineHeight = snapshot.scrollHeight;
    guard.minimumHeight = snapshot.scrollHeight;
  }
}

export function transcriptReaderExtentHasCollapsed(guard: TranscriptReaderExtentGuard): boolean {
  const threshold = Math.max(MIN_REVERSE_JUMP_PX, guard.clientHeight * REVERSE_JUMP_VIEWPORT_RATIO);
  return guard.acceptedHeight - guard.minimumHeight >= threshold;
}

export function transcriptReaderExtentReverseDelta(
  guard: TranscriptReaderExtentGuard,
  snapshot: TranscriptExtentSnapshot,
): number {
  const acceptedReverse = guard.direction > 0
    ? guard.acceptedTop - snapshot.scrollTop
    : snapshot.scrollTop - guard.acceptedTop;
  const blankReverse = guard.blankTop === undefined
    ? 0
    : guard.direction * (guard.blankTop - snapshot.scrollTop);
  return Math.max(acceptedReverse, blankReverse);
}

export function transcriptReaderAnchorReverseDelta(
  guard: TranscriptReaderExtentGuard,
  snapshot: TranscriptExtentSnapshot,
  currentAnchorOffset?: number,
): number {
  if (
    !guard.anchor
    || guard.anchorScrollTop === undefined
    || currentAnchorOffset === undefined
    || !Number.isFinite(currentAnchorOffset)
  ) return 0;
  const physicalTargetOffset = guard.anchor.offset - (snapshot.scrollTop - guard.anchorScrollTop);
  return guard.direction * (currentAnchorOffset - physicalTargetOffset);
}

export function transcriptReaderExtentCanCorrect(
  guard: TranscriptReaderExtentGuard,
  snapshot: TranscriptExtentSnapshot,
  currentAnchorOffset?: number,
): boolean {
  if (Math.abs(snapshot.clientHeight - guard.clientHeight) > 1) return false;
  if (
    transcriptReaderExtentReverseDelta(guard, snapshot) < MIN_REVERSE_JUMP_PX
    && transcriptReaderAnchorReverseDelta(guard, snapshot, currentAnchorOffset) < MIN_REVERSE_JUMP_PX
  ) return false;
  const extentGrowth = snapshot.scrollHeight - guard.acceptedHeight;
  const physicalMovement = snapshot.scrollTop - guard.acceptedTop;
  const anchorCompensationTolerance = Math.max(8, guard.clientHeight * 0.1);
  if (
    extentGrowth > 0
    && Math.abs(physicalMovement - extentGrowth) <= anchorCompensationTolerance
  ) return false;
  if (!guard.collapsed) return true;
  const reboundTolerance = Math.max(8, guard.clientHeight * EXTENT_REBOUND_VIEWPORT_RATIO);
  return snapshot.scrollHeight >= guard.acceptedHeight - reboundTolerance;
}

export function resolveTranscriptReaderExtentCorrection(
  guard: TranscriptReaderExtentGuard,
  snapshot: TranscriptExtentSnapshot,
  currentAnchorOffset?: number,
): number | undefined {
  if (!transcriptReaderExtentCanCorrect(guard, snapshot, currentAnchorOffset)) return undefined;
  const maxTop = Math.max(0, snapshot.scrollHeight - snapshot.clientHeight);
  const nativeReverse = transcriptReaderExtentReverseDelta(guard, snapshot);
  const physicalTargetOffset = guard.anchor && guard.anchorScrollTop !== undefined
    ? guard.anchor.offset - (snapshot.scrollTop - guard.anchorScrollTop)
    : undefined;
  const targetAnchorOffset = nativeReverse >= MIN_REVERSE_JUMP_PX
    ? guard.targetAnchorOffset
    : physicalTargetOffset;
  const anchorTarget = guard.anchor
    && targetAnchorOffset !== undefined
    && currentAnchorOffset !== undefined
    && Number.isFinite(currentAnchorOffset)
    ? snapshot.scrollTop + currentAnchorOffset - targetAnchorOffset
    : nativeReverse >= MIN_REVERSE_JUMP_PX && guard.blankTop !== undefined
      ? guard.blankTop
      : guard.expectedTop;
  const targetTop = clamp(anchorTarget, 0, maxTop);
  const correction = targetTop - snapshot.scrollTop;
  return guard.direction * correction > DIRECTION_JITTER_PX ? correction : undefined;
}

export function acceptTranscriptReaderExtentCorrection(
  guard: TranscriptReaderExtentGuard,
  snapshot: TranscriptExtentSnapshot,
  correction: number,
): void {
  const maxTop = Math.max(0, snapshot.scrollHeight - snapshot.clientHeight);
  const acceptedTop = clamp(snapshot.scrollTop + correction, 0, maxTop);
  guard.acceptedTop = acceptedTop;
  guard.acceptedHeight = snapshot.scrollHeight;
  guard.baselineTop = acceptedTop;
  guard.baselineHeight = snapshot.scrollHeight;
  guard.minimumHeight = snapshot.scrollHeight;
  guard.expectedTop = acceptedTop;
  guard.collapsed = false;
  guard.anomalyReported = false;
  guard.anchor = undefined;
  guard.anchorScrollTop = undefined;
  guard.targetAnchorOffset = undefined;
  guard.blankTop = undefined;
}

export function transcriptKeyboardScrollDelta(
  key: string,
  shiftKey: boolean,
  snapshot: TranscriptExtentSnapshot,
): number | undefined {
  const page = Math.max(1, snapshot.clientHeight * 0.9);
  switch (key) {
    case "ArrowUp": return -40;
    case "ArrowDown": return 40;
    case "PageUp": return -page;
    case "PageDown": return page;
    case "Home": return -snapshot.scrollTop;
    case "End": return Math.max(0, snapshot.scrollHeight - snapshot.clientHeight - snapshot.scrollTop);
    case " ":
    case "Spacebar":
      return shiftKey ? -page : page;
    default:
      return undefined;
  }
}

export function transcriptScrollEventCancelsReaderExtentGuard(type: TranscriptScrollEvent["type"]): boolean {
  return type === "RESET"
    || type === "MANUAL_READING"
    || type === "NATIVE_SCROLLBAR_BEGIN"
    || type === "USER_RESIZE_BEGIN"
    || type === "SELECTION_BEGIN"
    || type === "PROGRAMMATIC_BEGIN"
    || type === "JUMP_TO_BOTTOM"
    || type === "JUMP_TO_INDEX"
    || type === "SCROLL_TO_OFFSET"
    || type === "RECOVERY_BEGIN";
}
