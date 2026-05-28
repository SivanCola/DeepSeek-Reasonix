export const GOAL_STATE_FILENAME = ".reasonix-goal.json";
export const DEFAULT_GOAL_MAX_ATTEMPTS = 6;
export const DEFAULT_GOAL_TEST_TIMEOUT_MS = 120_000;

export type GoalStatus = "running" | "completed" | "failed";

export interface GoalFinding {
  attempt: number;
  finding: string;
}

export interface GoalReasoningSnapshot {
  attempt: number;
  finding: string;
}

export interface GoalState {
  goal: string;
  attempt: number;
  maxAttempts: number;
  status: GoalStatus;
  createdAt: string;
  updatedAt: string;
  prefixHash?: string;
  lastError?: string;
  lastTestResult?: string;
  failedSolutions: string[];
  findings: GoalFinding[];
  reasoningSnapshots: GoalReasoningSnapshot[];
}

export type TestProjectKind = "node" | "python" | "go" | "rust";

export interface DetectedTestCommand {
  kind: TestProjectKind;
  command: string;
  args: string[];
  display: string;
  reason: string;
}

export interface VerificationCounts {
  passed?: number;
  failed?: number;
}

export interface AutoTestResult {
  status: "passed" | "failed" | "skipped";
  command?: DetectedTestCommand;
  exitCode?: number | null;
  signal?: NodeJS.Signals | null;
  timedOut?: boolean;
  durationMs: number;
  outputTail: string;
  counts: VerificationCounts;
  reason?: string;
}

export type GoalSlashAction =
  | { kind: "start"; goal: string }
  | { kind: "resume" }
  | { kind: "stop" };
