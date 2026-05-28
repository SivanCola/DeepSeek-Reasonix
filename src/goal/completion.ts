const GOAL_COMPLETED_RE = /\bGOAL_COMPLETED\b/i;

export function isGoalCompleted(text: string | null | undefined): boolean {
  return GOAL_COMPLETED_RE.test(text ?? "");
}

export function stripGoalCompletedMarker(text: string): string {
  return text.replace(GOAL_COMPLETED_RE, "").trim();
}
