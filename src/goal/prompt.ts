import type { AutoTestResult, GoalFinding, GoalState } from "./types.js";

export const GOAL_PREFIX_BEGIN = "\n\n# Reasonix Goal Mode\n";
export const GOAL_PREFIX_END = "\n# End Reasonix Goal Mode";

export function buildStableGoalPrefix(goal: string): string {
  return [
    GOAL_PREFIX_BEGIN.trimStart(),
    "GOAL:",
    goal.trim(),
    "",
    "RULES:",
    "1. Analyze the problem before changing code.",
    "2. Modify the code needed to satisfy the goal.",
    "3. Verify the result with available tests or a concrete check.",
    "4. If the goal is not complete, continue with a different approach.",
    "5. When and only when the goal is complete, output GOAL_COMPLETED.",
    "",
    "COMPLETION RULES:",
    "- Output GOAL_COMPLETED only after code changes and verification are complete.",
    "- If tests fail or any issue remains unresolved, do not output GOAL_COMPLETED.",
    "- When complete, include the reason and verification result.",
    "- Do not repeat failed solutions listed in the dynamic Goal tail.",
    "- End each attempt with one short Finding: line. Summarize conclusions only; never expose hidden chain-of-thought.",
    GOAL_PREFIX_END.trim(),
  ].join("\n");
}

export function removeStableGoalPrefix(system: string): string {
  const start = system.indexOf(GOAL_PREFIX_BEGIN);
  if (start < 0) return system;
  const end = system.indexOf(GOAL_PREFIX_END, start);
  if (end < 0) return system.slice(0, start).trimEnd();
  return `${system.slice(0, start)}${system.slice(end + GOAL_PREFIX_END.length)}`.trimEnd();
}

export function withStableGoalPrefix(system: string, goal: string): string {
  const base = removeStableGoalPrefix(system).trimEnd();
  return `${base}\n\n${buildStableGoalPrefix(goal)}`;
}

export function buildInitialGoalInput(state: GoalState): string {
  return [
    "Start Goal Mode now.",
    "",
    `Attempt: ${state.attempt} / ${state.maxAttempts}`,
    "",
    "Use the stable GOAL in the system prompt. Analyze, edit, and verify. If you need tools, call them now.",
    "Do not output GOAL_COMPLETED until the goal is genuinely finished and verified.",
  ].join("\n");
}

export function buildGoalContinuationInput(state: GoalState, lastAssistantText: string): string {
  const lines = [
    "Continue Goal Mode.",
    "",
    `Attempt: ${state.attempt} / ${state.maxAttempts}`,
    "",
    "The stable GOAL is still in the system prompt. Do not restate or change it.",
  ];
  const assistantSummary = firstMeaningfulLine(lastAssistantText);
  if (assistantSummary) {
    lines.push("", "Previous assistant outcome:", assistantSummary);
  }
  if (state.lastError) {
    lines.push("", "Latest failure reason:", state.lastError);
  }
  if (state.lastTestResult) {
    lines.push("", "Latest verification:", state.lastTestResult);
  }
  if (state.failedSolutions.length > 0) {
    lines.push("", "Do not repeat these failed approaches:");
    for (const [idx, item] of state.failedSolutions.entries()) lines.push(`${idx + 1}. ${item}`);
  }
  if (state.findings.length > 0) {
    lines.push("", "What we learned:");
    for (const item of state.findings.slice(-5)) {
      lines.push(`- Attempt ${item.attempt}: ${item.finding}`);
    }
  }
  lines.push(
    "",
    "Choose the next smallest useful action. If complete and verified, output GOAL_COMPLETED with a short reason.",
  );
  return lines.join("\n");
}

export function firstMeaningfulLine(text: string, maxChars = 220): string {
  const line =
    text
      .split(/\r?\n/)
      .map((l) => l.trim())
      .find((l) => l.length > 0 && !/^GOAL_COMPLETED\b/i.test(l)) ?? "";
  return line.length > maxChars ? `${line.slice(0, maxChars - 3)}...` : line;
}

export function extractGoalFindings(text: string, attempt: number): GoalFinding[] {
  const findings: GoalFinding[] = [];
  const direct = /^(?:Finding|Learned|What I learned):\s*(.+)$/gim;
  for (const match of text.matchAll(direct)) {
    const finding = cleanFinding(match[1] ?? "");
    if (finding) findings.push({ attempt, finding });
  }

  const block = /What did I learn\?\s*([\s\S]{0,800})/i.exec(text)?.[1];
  if (block) {
    for (const line of block.split(/\r?\n/).slice(0, 6)) {
      const finding = cleanFinding(line.replace(/^[-*]\s*/, ""));
      if (finding) findings.push({ attempt, finding });
    }
  }

  const seen = new Set<string>();
  return findings
    .filter((item) => {
      const key = item.finding.toLowerCase();
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    })
    .slice(0, 3);
}

export function buildFailedSolutionNote(
  attempt: number,
  assistantText: string,
  testResult: AutoTestResult | null,
): string {
  if (testResult?.status === "failed") {
    return `Attempt ${attempt} failed verification: ${summarizeAutoTestFailure(testResult)}`;
  }
  const line = firstMeaningfulLine(assistantText, 180);
  return line
    ? `Attempt ${attempt} did not complete: ${line}`
    : `Attempt ${attempt} did not complete and produced no actionable summary`;
}

export function summarizeAutoTestFailure(result: AutoTestResult): string {
  if (result.timedOut) return `${result.command?.display ?? "test command"} timed out`;
  const tail = firstMeaningfulLine(result.outputTail, 160);
  if (tail) return tail;
  if (result.exitCode !== undefined) return `exit code ${result.exitCode}`;
  return "verification failed";
}

export function buildGoalFinalSummary(
  state: GoalState,
  filesChanged: readonly string[] = [],
): string {
  const title = state.status === "completed" ? "Goal Completed" : "Goal Failed";
  const lines = [
    title,
    "",
    `Goal: ${state.goal}`,
    `Attempts: ${state.attempt} / ${state.maxAttempts}`,
  ];
  if (filesChanged.length > 0) {
    lines.push("", "Files Changed:");
    for (const file of filesChanged.slice(0, 20)) lines.push(`- ${file}`);
  }
  if (state.lastTestResult) lines.push("", "Verification:", state.lastTestResult);
  if (state.lastError) lines.push("", "Result:", state.lastError);
  if (state.failedSolutions.length > 0) {
    lines.push("", "Failed approaches:");
    for (const item of state.failedSolutions.slice(-5)) lines.push(`- ${item}`);
  }
  if (state.findings.length > 0) {
    lines.push("", "Findings:");
    for (const item of state.findings.slice(-5)) lines.push(`- ${item.finding}`);
  }
  return lines.join("\n");
}

function cleanFinding(value: string): string {
  const cleaned = value.replace(/\s+/g, " ").trim();
  if (!cleaned) return "";
  return cleaned.length > 240 ? `${cleaned.slice(0, 237)}...` : cleaned;
}
