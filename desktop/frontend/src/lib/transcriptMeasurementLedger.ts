export type TranscriptMeasurementChange = {
  key: string;
  size: number;
};

/**
 * Immutable, block-keyed DOM measurement snapshots for the Transcript window.
 * A render can observe either the old snapshot or the complete new snapshot,
 * never the partially-updated prefix tree produced by per-item publication.
 */
export class TranscriptMeasurementLedger {
  private sizes: ReadonlyMap<string, number> = new Map();

  sizeFor(key: string, fallback: number): number {
    return this.sizes.get(key) ?? fallback;
  }

  commit(changes: readonly TranscriptMeasurementChange[]): boolean {
    if (changes.length === 0) return false;
    const next = new Map(this.sizes);
    let changed = false;
    for (const change of changes) {
      if (!change.key || !Number.isFinite(change.size) || change.size <= 0) continue;
      if (Math.abs((next.get(change.key) ?? 0) - change.size) <= 0.5) continue;
      next.set(change.key, change.size);
      changed = true;
    }
    if (!changed) return false;
    this.sizes = next;
    return true;
  }

  retain(keys: ReadonlySet<string>): boolean {
    if (this.sizes.size === 0) return false;
    const next = new Map<string, number>();
    for (const [key, size] of this.sizes) {
      if (keys.has(key)) next.set(key, size);
    }
    if (next.size === this.sizes.size) return false;
    this.sizes = next;
    return true;
  }
}
