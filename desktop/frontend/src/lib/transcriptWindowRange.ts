export type TranscriptWindowItem = {
  index: number;
  start: number;
  end: number;
};

export type TranscriptWindowRangeSource = "candidate" | "retained" | "reconstructed";

export type TranscriptWindowRange<T extends TranscriptWindowItem> = {
  structureRevision: string;
  scrollTop: number;
  scrollMargin: number;
  totalSize: number;
  items: readonly T[];
  source: TranscriptWindowRangeSource;
};

function coversColdViewport<T extends TranscriptWindowItem>(
  items: readonly T[],
  scrollTop: number,
  clientHeight: number,
  coldStart: number,
  coldEnd: number,
): boolean {
  const start = Math.max(scrollTop, coldStart);
  const end = Math.min(scrollTop + clientHeight, coldEnd);
  if (end <= start) return true;
  let cursor = start;
  for (const item of [...items].sort((left, right) => left.start - right.start)) {
    if (item.end <= cursor) continue;
    if (item.start > cursor + 0.5) return false;
    cursor = Math.max(cursor, item.end);
    if (cursor >= end - 0.5) return true;
  }
  return false;
}

function reconstructRange<T extends TranscriptWindowItem>(
  measurements: readonly T[],
  retainedIndexes: ReadonlySet<number>,
  scrollTop: number,
  clientHeight: number,
  coldStart: number,
  coldEnd: number,
  overscan: number,
): readonly T[] {
  const start = Math.max(scrollTop, coldStart);
  const end = Math.min(scrollTop + clientHeight, coldEnd);
  if (end <= start) return [];
  const first = measurements.findIndex((item) => item.end > start);
  if (first < 0) return [];
  let last = first;
  while (last + 1 < measurements.length && measurements[last + 1].start < end) last += 1;
  const indexes = new Set<number>();
  for (let index = Math.max(0, first - overscan); index <= Math.min(measurements.length - 1, last + overscan); index += 1) {
    indexes.add(index);
  }
  retainedIndexes.forEach((index) => indexes.add(index));
  return Array.from(indexes)
    .sort((left, right) => left - right)
    .map((index) => measurements[index])
    .filter((item): item is T => Boolean(item));
}

export function commitTranscriptWindowRange<T extends TranscriptWindowItem>({
  candidate,
  measurements,
  retainedIndexes,
  previous,
  structureRevision,
  scrollTop,
  clientHeight,
  scrollMargin,
  totalSize,
  overscan,
  gestureActive,
}: {
  candidate: readonly T[];
  measurements: readonly T[];
  retainedIndexes: ReadonlySet<number>;
  previous?: TranscriptWindowRange<T>;
  structureRevision: string;
  scrollTop: number;
  clientHeight: number;
  scrollMargin: number;
  totalSize: number;
  overscan: number;
  gestureActive: boolean;
}): TranscriptWindowRange<T> {
  const coldStart = scrollMargin;
  const coldEnd = scrollMargin + totalSize;
  const next: TranscriptWindowRange<T> = { structureRevision, scrollTop, scrollMargin, totalSize, items: candidate, source: "candidate" };
  const sameStructure = previous?.structureRevision === structureRevision;
  const sameMargin = previous != null && Math.abs(previous.scrollMargin - scrollMargin) <= 0.5;
  const previousCovers = Boolean(sameStructure && sameMargin && coversColdViewport(
    previous.items,
    scrollTop,
    clientHeight,
    previous.scrollMargin,
    previous.scrollMargin + previous.totalSize,
  ));
  const candidateCovers = coversColdViewport(candidate, scrollTop, clientHeight, coldStart, coldEnd);

  // A measurement-only notification must not move the painted reader range
  // while native input still owns the unchanged viewport.
  if (previous && previousCovers && gestureActive && Math.abs(previous.scrollTop - scrollTop) <= 0.5) {
    return { ...previous, source: "retained" };
  }
  if (candidateCovers) return next;

  // Native WebViews may deliver a stale range notification after a newer
  // scroll position was already painted. Retain the last covering range until
  // TanStack produces a candidate that covers the authoritative native view.
  if (previous && previousCovers) {
    return { ...previous, scrollTop, source: "retained" };
  }

  // A large native jump can invalidate both the candidate and the previously
  // painted range. Rebuild synchronously from TanStack's prefix-size ledger so
  // the adapter never commits an uncovered viewport while waiting for its next
  // asynchronous range notification.
  const reconstructed = reconstructRange(measurements, retainedIndexes, scrollTop, clientHeight, coldStart, coldEnd, overscan);
  if (coversColdViewport(reconstructed, scrollTop, clientHeight, coldStart, coldEnd)) {
    return { structureRevision, scrollTop, scrollMargin, totalSize, items: reconstructed, source: "reconstructed" };
  }
  return next;
}
