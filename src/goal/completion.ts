export function isGoalCompleted(text: string | null | undefined): boolean {
  return findGoalCompletedMarkerLine(text ?? "") >= 0;
}

export function stripGoalCompletedMarker(text: string): string {
  const markerLine = findGoalCompletedMarkerLine(text);
  if (markerLine < 0) return text.trim();
  return text
    .split(/\r?\n/)
    .filter((_, index) => index !== markerLine)
    .join("\n")
    .trim();
}

function findGoalCompletedMarkerLine(text: string): number {
  let inFence = false;
  const lines = text.split(/\r?\n/);
  for (const [index, line] of lines.entries()) {
    const trimmed = line.trim();
    if (/^```/.test(trimmed) || /^~~~/.test(trimmed)) {
      inFence = !inFence;
      continue;
    }
    if (!inFence && /^GOAL_COMPLETED$/i.test(trimmed)) return index;
  }
  return -1;
}
