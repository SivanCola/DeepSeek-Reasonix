import {
  isSubstantialTranscriptDisplacement,
  type TranscriptScrollEvent,
  type TranscriptScrollState,
} from "./transcriptScrollArbiter";
import {
  hasTranscriptScrollableRange,
  nativeTranscriptDistanceFromBottom,
  TRANSCRIPT_AT_BOTTOM_THRESHOLD_PX,
} from "./transcriptScrollGeometry";

type MutableRef<T> = { current: T };
type NativeExtent = { scrollHeight: number; clientHeight: number };
// Native ranges can settle well before a loaded CI WebView mounts LAST. Keep
// the explicit bottom-release proof bounded to roughly two seconds at 60fps.
export const MAX_TAIL_MOUNT_CHECKS = 120;

function tailIsMounted(element: HTMLElement): boolean {
  if (element.querySelector("[data-live-region='true']")) return true;
  const totalRows = Number.parseInt(element.dataset.transcriptRowCount ?? "", 10);
  const firstItemIndex = Number.parseInt(element.dataset.transcriptFirstItemIndex ?? "", 10);
  if (!Number.isFinite(totalRows) || !Number.isFinite(firstItemIndex)) {
    return element.querySelector(".transcript__row") !== null;
  }
  if (totalRows <= 0) return false;
  const tailIndex = firstItemIndex + totalRows - 1;
  return Array.from(element.querySelectorAll<HTMLElement>(".transcript__row[data-item-index]"))
    .some((row) => Number.parseInt(row.dataset.itemIndex ?? "", 10) === tailIndex);
}

function extentChanged(held: NativeExtent, element: HTMLElement): boolean {
  return Math.abs(held.scrollHeight - element.scrollHeight) > 1
    || Math.abs(held.clientHeight - element.clientHeight) > 1;
}

export type TranscriptReaderBottomHold = readonly [
  cancel: () => void,
  deliver: (element: HTMLDivElement) => void,
];

export function createTranscriptReaderBottomHold({
  scrollRef,
  stateRef,
  generationRef,
  deliverScrollRef,
  dispatch,
}: {
  scrollRef: MutableRef<HTMLDivElement | null>;
  stateRef: MutableRef<TranscriptScrollState>;
  generationRef: MutableRef<number>;
  deliverScrollRef: MutableRef<((element?: HTMLDivElement) => void) | null>;
  dispatch: (event: TranscriptScrollEvent) => void;
}): TranscriptReaderBottomHold {
  let frame: number | null = null;
  let heldExtent: NativeExtent | null = null;
  let totalChecks = 0;

  const cancel = () => {
    if (frame !== null) cancelAnimationFrame(frame);
    frame = null;
    heldExtent = null;
    totalChecks = 0;
  };

  const deliver = (element: HTMLDivElement) => {
    const distance = nativeTranscriptDistanceFromBottom(element);
    const atBottom = distance <= TRANSCRIPT_AT_BOTTOM_THRESHOLD_PX;
    const tailMounted = tailIsMounted(element);
    if (heldExtent && extentChanged(heldExtent, element)) {
      // The earlier bottom sample belongs to a different native extent.
      if (frame !== null) cancelAnimationFrame(frame);
      frame = null;
      heldExtent = null;
      dispatch({
        type: "SCROLL_DELIVERED",
        atBottom: false,
        scrollable: hasTranscriptScrollableRange(element),
        substantial: isSubstantialTranscriptDisplacement(distance),
        tailMounted,
      });
    }
    dispatch({
      type: "SCROLL_DELIVERED",
      atBottom,
      scrollable: hasTranscriptScrollableRange(element),
      substantial: isSubstantialTranscriptDisplacement(distance),
      tailMounted,
    });

    const state = stateRef.current;
    if (
      atBottom
      && state.mode === "reader-gesture"
      && state.readerIntent
      && state.readerIntentCanClaimTail
      && frame === null
    ) {
      if (totalChecks >= MAX_TAIL_MOUNT_CHECKS) {
        // The native thumb proved the physical end, but a loaded WebView can
        // leave LAST unmounted or keep revising its extent beyond the passive
        // observation budget. Hand that bounded failure to the existing jump-tail
        // transaction instead of abandoning the reader in manual mode. This
        // is one arbiter-owned command after release, never a direct write.
        cancel();
        dispatch({ type: "JUMP_TO_BOTTOM" });
        return;
      }
      const generation = generationRef.current;
      heldExtent = tailMounted ? { scrollHeight: element.scrollHeight, clientHeight: element.clientHeight } : null;
      totalChecks += 1;
      frame = requestAnimationFrame(() => {
        const held = heldExtent;
        frame = null;
        heldExtent = null;
        const current = stateRef.current;
        if (
          generationRef.current !== generation
          || scrollRef.current !== element
          || current.mode !== "reader-gesture"
          || !current.readerIntent
          || !current.readerIntentCanClaimTail
        ) return;
        if (held && extentChanged(held, element)) {
          const currentDistance = nativeTranscriptDistanceFromBottom(element);
          dispatch({
            type: "SCROLL_DELIVERED",
            atBottom: false,
            scrollable: hasTranscriptScrollableRange(element),
            substantial: isSubstantialTranscriptDisplacement(currentDistance),
            tailMounted: tailIsMounted(element),
          });
        }
        deliverScrollRef.current?.(element);
      });
    } else if (!atBottom) {
      cancel();
    }
  };

  return [cancel, deliver];
}
