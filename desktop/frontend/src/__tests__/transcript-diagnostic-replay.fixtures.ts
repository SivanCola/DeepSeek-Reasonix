// Content-free geometry distilled from the two field diagnostics. The source
// exports are intentionally not checked in: only viewport/extents and gesture
// deltas needed to reproduce the races remain here.

export const nativeDownwardCollapseReplay = {
  baseline: { scrollTop: 14_567.47, scrollHeight: 15_829, clientHeight: 725 },
  gestureDeltas: [16, 16, 32, 48, 133.33] as const,
  collapsed: { scrollTop: 12_618.67, scrollHeight: 13_344, clientHeight: 725 },
  rebound: { scrollTop: 12_618.67, scrollHeight: 15_829, clientHeight: 725 },
} as const;

export const reasoningExtentReplay = {
  clientHeight: 778,
  extents: [19_037, 12_618, 19_012, 24_627, 19_008] as const,
} as const;
