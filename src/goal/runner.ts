import type { CacheFirstLoop } from "../loop.js";
import { formatAutoTestResult, runGoalAutoTest } from "./auto-test.js";
import { isGoalCompleted } from "./completion.js";
import {
  buildFailedSolutionNote,
  buildGoalContinuationInput,
  buildGoalFinalSummary,
  buildInitialGoalInput,
  extractGoalFindings,
  removeStableGoalPrefix,
  summarizeAutoTestFailure,
  withStableGoalPrefix,
} from "./prompt.js";
import { appendUniqueLimited, saveGoalState, touchGoalState } from "./state.js";
import type { AutoTestResult, GoalFinding, GoalState } from "./types.js";

export interface GoalRunResult {
  state: GoalState;
  completed: boolean;
  stopped: boolean;
  summary: string;
}

export interface RunGoalLoopOptions {
  rootDir: string;
  loop: CacheFirstLoop;
  state: GoalState;
  runTurn: (input: string) => Promise<string>;
  onInfo?: (message: string) => void;
  shouldStop?: () => boolean;
  runTests?: boolean;
  testTimeoutMs?: number;
  getFilesChanged?: () => readonly string[];
  restorePrefixOnExit?: boolean;
}

export async function runGoalLoop(opts: RunGoalLoopOptions): Promise<GoalRunResult> {
  let state = touchGoalState(opts.state);
  const baseSystem = removeStableGoalPrefix(opts.loop.prefix.system);
  opts.loop.prefix.replaceSystem(withStableGoalPrefix(baseSystem, state.goal));
  state = touchGoalState({ ...state, prefixHash: opts.loop.prefix.fingerprint });
  saveGoalState(opts.rootDir, state);
  opts.onInfo?.(
    [
      "Goal started",
      `Goal: ${state.goal}`,
      `Attempt: ${state.attempt} / ${state.maxAttempts}`,
      `Prefix: ${state.prefixHash}`,
    ].join("\n"),
  );

  let lastAssistantText = "";
  let stopped = false;

  try {
    while (state.status === "running" && state.attempt < state.maxAttempts) {
      if (opts.shouldStop?.()) {
        stopped = true;
        break;
      }

      const attempt = state.attempt + 1;
      state = touchGoalState({ ...state, attempt });
      saveGoalState(opts.rootDir, state);
      opts.onInfo?.(formatGoalProgress(state, "Running", opts.getFilesChanged?.()));

      const input =
        attempt === 1
          ? buildInitialGoalInput(state)
          : buildGoalContinuationInput(state, lastAssistantText);

      try {
        lastAssistantText = await opts.runTurn(input);
      } catch (err) {
        state = touchGoalState({
          ...state,
          status: "failed",
          lastError: err instanceof Error ? err.message : String(err),
        });
        saveGoalState(opts.rootDir, state);
        break;
      }

      if (opts.shouldStop?.()) {
        stopped = true;
        break;
      }

      const testResult = await maybeRunTests(opts, state);
      const testSummary = testResult ? formatAutoTestResult(testResult) : undefined;
      const findings = mergeFindings(
        state.findings,
        extractGoalFindings(lastAssistantText, attempt),
      );
      const reasoningSnapshots = mergeFindings(
        state.reasoningSnapshots,
        findings.slice(-3).map((item) => ({ attempt: item.attempt, finding: item.finding })),
      );
      const completedByModel = isGoalCompleted(lastAssistantText);
      const verificationPassed = !testResult || testResult.status === "passed";
      const verificationSkipped = testResult?.status === "skipped";

      if (completedByModel && (verificationPassed || verificationSkipped)) {
        state = touchGoalState({
          ...state,
          status: "completed",
          lastTestResult: testSummary,
          lastError: undefined,
          findings,
          reasoningSnapshots,
        });
        saveGoalState(opts.rootDir, state);
        break;
      }

      const failedNote = buildFailedSolutionNote(attempt, lastAssistantText, testResult);
      const lastError =
        completedByModel && testResult?.status === "failed"
          ? `GOAL_COMPLETED was rejected because verification failed: ${summarizeAutoTestFailure(testResult)}`
          : failedNote;

      state = touchGoalState({
        ...state,
        lastError,
        lastTestResult: testSummary,
        failedSolutions: appendUniqueLimited(state.failedSolutions, failedNote, 8),
        findings,
        reasoningSnapshots,
      });
      saveGoalState(opts.rootDir, state);
      opts.onInfo?.(formatGoalProgress(state, "Continuing", opts.getFilesChanged?.()));
    }

    if (stopped) {
      state = touchGoalState({
        ...state,
        status: "failed",
        lastError: "Goal stopped by user",
      });
      saveGoalState(opts.rootDir, state);
    } else if (state.status === "running" && state.attempt >= state.maxAttempts) {
      state = touchGoalState({
        ...state,
        status: "failed",
        lastError: `Reached max attempts (${state.maxAttempts}) without GOAL_COMPLETED`,
      });
      saveGoalState(opts.rootDir, state);
    }
  } finally {
    if (opts.restorePrefixOnExit !== false) {
      opts.loop.prefix.replaceSystem(baseSystem);
    }
  }

  const summary = buildGoalFinalSummary(state, opts.getFilesChanged?.() ?? []);
  opts.onInfo?.(summary);
  return {
    state,
    completed: state.status === "completed",
    stopped,
    summary,
  };
}

export function lastAssistantTextFromLoop(loop: CacheFirstLoop): string {
  const messages = loop.log.toFullHistory();
  for (let i = messages.length - 1; i >= 0; i--) {
    const msg = messages[i];
    if (msg?.role !== "assistant") continue;
    return typeof msg.content === "string" ? msg.content : "";
  }
  return "";
}

function formatGoalProgress(
  state: GoalState,
  status: string,
  filesChanged: readonly string[] = [],
): string {
  const lines = [
    `Goal: ${state.goal}`,
    `Attempt: ${state.attempt} / ${state.maxAttempts}`,
    `Status: ${status}`,
  ];
  if (filesChanged.length > 0) {
    lines.push("Files Changed:");
    for (const file of filesChanged.slice(0, 12)) lines.push(`- ${file}`);
  }
  if (state.prefixHash) lines.push(`Prefix: ${state.prefixHash}`);
  if (state.lastError) lines.push(`Last error: ${state.lastError}`);
  return lines.join("\n");
}

async function maybeRunTests(
  opts: RunGoalLoopOptions,
  state: GoalState,
): Promise<AutoTestResult | null> {
  if (opts.runTests === false) return null;
  opts.onInfo?.(formatGoalProgress(state, "Testing", opts.getFilesChanged?.()));
  return await runGoalAutoTest(opts.rootDir, { timeoutMs: opts.testTimeoutMs });
}

function mergeFindings<T extends GoalFinding>(current: readonly T[], next: readonly T[]): T[] {
  const out: T[] = [...current];
  for (const item of next) {
    const key = item.finding.toLowerCase();
    if (out.some((existing) => existing.finding.toLowerCase() === key)) continue;
    out.push(item);
  }
  return out.slice(-12);
}
