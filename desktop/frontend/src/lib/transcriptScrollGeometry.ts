import { isTranscriptContentShrink } from "./transcriptScrollArbiter";

export const TRANSCRIPT_AT_BOTTOM_THRESHOLD_PX = 4;

export type TranscriptFollowGeometry = {
  contentExtent: number | null;
  viewportExtent: number | null;
};

type NativeTranscriptGeometry = {
  scrollHeight: number;
  clientHeight: number;
  offsetHeight?: number;
};

/** WebView2 can under-report clientHeight while clamping the transcript's
 * scrollTop against its painted border box. Transcript has no block border or
 * horizontal scrollbar, so the larger extent is the reachable viewport. */
export function nativeTranscriptViewportExtent(element: NativeTranscriptGeometry) {
  const offsetHeight = Number.isFinite(element.offsetHeight) ? element.offsetHeight ?? 0 : 0;
  return Math.max(0, element.clientHeight, offsetHeight);
}

export function nativeTranscriptDistanceFromBottom(element: {
  scrollHeight: number;
  scrollTop: number;
  clientHeight: number;
  offsetHeight?: number;
}) {
  return element.scrollHeight - element.scrollTop - nativeTranscriptViewportExtent(element);
}

export function nativeTranscriptBottomTop(element: NativeTranscriptGeometry) {
  return Math.max(0, element.scrollHeight - nativeTranscriptViewportExtent(element));
}

export function hasTranscriptScrollableRange(
  element: NativeTranscriptGeometry,
  threshold = TRANSCRIPT_AT_BOTTOM_THRESHOLD_PX,
) {
  return nativeTranscriptBottomTop(element) > threshold;
}

export function pinTranscriptTailAfterViewportShrink(
  element: { scrollHeight: number; scrollTop: number; clientHeight: number; offsetHeight?: number },
  geometry: TranscriptFollowGeometry,
  tailFollow: boolean,
): number | null {
  const viewport = nativeTranscriptViewportExtent(element);
  const viewportShrunk = geometry.viewportExtent != null
    && geometry.viewportExtent - viewport > TRANSCRIPT_AT_BOTTOM_THRESHOLD_PX;
  geometry.viewportExtent = viewport;
  const contentShrunk = geometry.contentExtent != null
    && isTranscriptContentShrink(element.scrollHeight - geometry.contentExtent);
  if (!tailFollow || !viewportShrunk || contentShrunk) return null;
  const bottom = nativeTranscriptBottomTop(element);
  return bottom - element.scrollTop > TRANSCRIPT_AT_BOTTOM_THRESHOLD_PX ? bottom : null;
}
