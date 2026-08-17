export type NavigationSurfaceIntent = number | null;

/** Older completions must never release the latest navigation surface mask. */
export function settleNavigationSurfaceIntent(
  current: NavigationSurfaceIntent,
  completedIntent: number,
): NavigationSurfaceIntent {
  return current === completedIntent ? null : current;
}
