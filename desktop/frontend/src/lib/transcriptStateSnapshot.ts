import type { StateSnapshot } from "react-virtuoso";

/**
 * One captured Virtuoso state snapshot bound to the row key sequence it was
 * taken from. Ranges are Virtuoso-internal size-tree indexes (data-relative);
 * the row keys are the source of truth for whether the snapshot still
 * describes the current data.
 */
export type TranscriptStateSnapshot = {
  keys: readonly string[];
  snapshot: StateSnapshot;
};

function prefixLength(keys: readonly string[], current: readonly string[]): number {
  let index = 0;
  while (index < keys.length && keys[index] === current[index]) index += 1;
  return index;
}

/**
 * Adapts a captured snapshot to the current row key sequence, or returns
 * undefined when the data no longer matches (the caller then falls back to
 * the measured-height estimates from transcriptMeasuredSizes).
 *
 * - identical keys: restore as-is (watchdog rebuild, same-tab reveal);
 * - appended rows (snapshot keys are a prefix): ranges and scrollTop stay
 *   valid, old rows kept their data indexes;
 * - prepended rows (snapshot keys are a suffix): old rows moved `delta` data
 *   indexes down, so the ranges translate by the same delta;
 * - anything else (rewind, session switch): discard.
 */
export function resolveTranscriptStateSnapshot(
  record: TranscriptStateSnapshot | null,
  currentKeys: readonly string[],
): StateSnapshot | undefined {
  if (!record || record.keys.length === 0 || currentKeys.length === 0) return undefined;
  if (prefixLength(record.keys, currentKeys) === record.keys.length) return record.snapshot;
  if (record.keys.length > currentKeys.length) return undefined;
  const delta = currentKeys.length - record.keys.length;
  for (let index = 0; index < record.keys.length; index += 1) {
    if (record.keys[index] !== currentKeys[index + delta]) return undefined;
  }
  return {
    scrollTop: record.snapshot.scrollTop,
    ranges: record.snapshot.ranges.map((range) => ({
      ...range,
      startIndex: range.startIndex + delta,
      endIndex: range.endIndex === Infinity ? Infinity : range.endIndex + delta,
    })),
  };
}
