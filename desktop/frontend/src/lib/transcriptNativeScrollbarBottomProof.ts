import {
  nativeTranscriptDistanceFromBottom,
  TRANSCRIPT_AT_BOTTOM_THRESHOLD_PX,
} from "./transcriptScrollGeometry";

type MutableRef<T> = { current: T };

export type TranscriptNativeScrollbarBottomProof = {
  begin: (element: HTMLDivElement) => void;
  finish: (element: HTMLDivElement | null) => boolean;
  cancel: () => void;
};

/**
 * Retains physical-bottom evidence for one native thumb transaction.
 *
 * Chromium/WebKit can update the native scroller before React delivers the
 * corresponding scroll event. A passive animation-frame sampler closes that
 * release race without writing scrollTop or changing arbiter ownership.
 */
export function createTranscriptNativeScrollbarBottomProof({
  scrollRef,
}: {
  scrollRef: MutableRef<HTMLDivElement | null>;
}): TranscriptNativeScrollbarBottomProof {
  let activeElement: HTMLDivElement | null = null;
  let initialTop = 0;
  let frame: number | null = null;
  let reachedBottom = false;

  const cancelFrame = () => {
    if (frame !== null) cancelAnimationFrame(frame);
    frame = null;
  };

  const sample = () => {
    frame = null;
    const element = activeElement;
    if (!element || scrollRef.current !== element) return;
    if (
      Math.abs(element.scrollTop - initialTop) > 1
      && nativeTranscriptDistanceFromBottom(element) <= TRANSCRIPT_AT_BOTTOM_THRESHOLD_PX
    ) reachedBottom = true;
    frame = requestAnimationFrame(sample);
  };

  const cancel = () => {
    cancelFrame();
    activeElement = null;
    reachedBottom = false;
  };

  const begin = (element: HTMLDivElement) => {
    cancel();
    activeElement = element;
    initialTop = element.scrollTop;
    frame = requestAnimationFrame(sample);
  };

  const finish = (element: HTMLDivElement | null) => {
    const proved = Boolean(
      element
      && activeElement === element
      && scrollRef.current === element
      && (reachedBottom || nativeTranscriptDistanceFromBottom(element) <= TRANSCRIPT_AT_BOTTOM_THRESHOLD_PX)
    );
    cancel();
    return proved;
  };

  return { begin, finish, cancel };
}
