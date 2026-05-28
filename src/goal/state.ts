import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { atomicWriteSync } from "../core/atomic-write.js";
import {
  DEFAULT_GOAL_MAX_ATTEMPTS,
  GOAL_STATE_FILENAME,
  type GoalFinding,
  type GoalReasoningSnapshot,
  type GoalState,
  type GoalStatus,
} from "./types.js";

export function goalStatePath(rootDir: string): string {
  return join(rootDir, GOAL_STATE_FILENAME);
}

export function resolveGoalMaxAttempts(raw: unknown): number {
  if (typeof raw === "number" && Number.isInteger(raw) && raw > 0) return raw;
  if (typeof raw === "string" && raw.trim()) {
    const n = Number.parseInt(raw, 10);
    if (Number.isInteger(n) && n > 0) return n;
  }
  return DEFAULT_GOAL_MAX_ATTEMPTS;
}

export function createGoalState(
  goal: string,
  opts: { maxAttempts?: number; now?: Date } = {},
): GoalState {
  const trimmed = goal.trim();
  if (!trimmed) throw new Error("goal text is required");
  const now = (opts.now ?? new Date()).toISOString();
  return {
    goal: trimmed,
    attempt: 0,
    maxAttempts: resolveGoalMaxAttempts(opts.maxAttempts),
    status: "running",
    createdAt: now,
    updatedAt: now,
    failedSolutions: [],
    findings: [],
    reasoningSnapshots: [],
  };
}

function stringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value.filter((v): v is string => typeof v === "string" && v.trim().length > 0);
}

function attemptNumber(value: unknown): number {
  const n = typeof value === "number" ? value : Number(value ?? 0);
  return Number.isFinite(n) && n >= 0 ? Math.floor(n) : 0;
}

function findingsArray(value: unknown): GoalFinding[] {
  if (!Array.isArray(value)) return [];
  const out: GoalFinding[] = [];
  for (const item of value) {
    if (!item || typeof item !== "object") continue;
    const raw = item as Record<string, unknown>;
    const attempt = attemptNumber(raw.attempt);
    const finding = typeof raw.finding === "string" ? raw.finding.trim() : "";
    if (!finding) continue;
    out.push({ attempt, finding });
  }
  return out;
}

function snapshotsArray(value: unknown): GoalReasoningSnapshot[] {
  if (!Array.isArray(value)) return [];
  const out: GoalReasoningSnapshot[] = [];
  for (const item of value) {
    if (!item || typeof item !== "object") continue;
    const raw = item as Record<string, unknown>;
    const attempt = attemptNumber(raw.attempt);
    const finding = typeof raw.finding === "string" ? raw.finding.trim() : "";
    if (!finding) continue;
    out.push({ attempt, finding });
  }
  return out;
}

function status(value: unknown): GoalStatus {
  return value === "completed" || value === "failed" || value === "running" ? value : "running";
}

export function normalizeGoalState(raw: unknown): GoalState | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return null;
  const obj = raw as Record<string, unknown>;
  const goal = typeof obj.goal === "string" ? obj.goal.trim() : "";
  if (!goal) return null;
  const now = new Date().toISOString();
  const createdAt = typeof obj.createdAt === "string" ? obj.createdAt : now;
  const updatedAt = typeof obj.updatedAt === "string" ? obj.updatedAt : createdAt;
  const maxAttempts = resolveGoalMaxAttempts(obj.maxAttempts ?? obj.max_attempts);
  const attempt = attemptNumber(obj.attempt);
  return {
    goal,
    attempt,
    maxAttempts,
    status: status(obj.status),
    createdAt,
    updatedAt,
    ...(typeof obj.prefixHash === "string" ? { prefixHash: obj.prefixHash } : {}),
    ...(typeof obj.lastError === "string" ? { lastError: obj.lastError } : {}),
    ...(typeof obj.lastTestResult === "string" ? { lastTestResult: obj.lastTestResult } : {}),
    failedSolutions: stringArray(obj.failedSolutions ?? obj.failed_solutions),
    findings: findingsArray(obj.findings),
    reasoningSnapshots: snapshotsArray(obj.reasoningSnapshots ?? obj.reasoning_snapshots),
  };
}

export interface LoadGoalStateResult {
  path: string;
  state: GoalState | null;
  error?: string;
}

export function loadGoalState(rootDir: string): LoadGoalStateResult {
  const path = goalStatePath(rootDir);
  if (!existsSync(path)) return { path, state: null };
  try {
    const parsed = JSON.parse(readFileSync(path, "utf8"));
    const state = normalizeGoalState(parsed);
    if (!state) return { path, state: null, error: "goal state file is invalid" };
    return { path, state };
  } catch (err) {
    return { path, state: null, error: err instanceof Error ? err.message : String(err) };
  }
}

export function saveGoalState(rootDir: string, state: GoalState): void {
  const path = goalStatePath(rootDir);
  const body = `${JSON.stringify({ ...state, updatedAt: state.updatedAt }, null, 2)}\n`;
  const tmp = `${path}.tmp-${process.pid}-${Date.now()}`;
  atomicWriteSync(path, body, tmp);
}

export function touchGoalState(state: GoalState, now: Date = new Date()): GoalState {
  return { ...state, updatedAt: now.toISOString() };
}

export function appendUniqueLimited(
  values: readonly string[],
  next: string | undefined,
  limit: number,
): string[] {
  const cleaned = next?.replace(/\s+/g, " ").trim();
  const out = values.filter((v) => v.trim().length > 0);
  if (!cleaned) return out.slice(-limit);
  const lower = cleaned.toLowerCase();
  const deduped = out.filter((v) => v.toLowerCase() !== lower);
  deduped.push(cleaned);
  return deduped.slice(-limit);
}

export function formatGoalStatus(state: GoalState | null, error?: string): string {
  if (error) return `Goal state could not be read: ${error}`;
  if (!state) {
    return [
      "No active Goal.",
      "",
      "Usage:",
      "  /goal <text>",
      "  /goal resume",
      "  /goal stop",
    ].join("\n");
  }
  const lines = [
    `Goal: ${state.goal}`,
    `Attempt: ${state.attempt} / ${state.maxAttempts}`,
    `Status: ${state.status}`,
  ];
  if (state.prefixHash) lines.push(`Prefix: ${state.prefixHash}`);
  if (state.lastError) lines.push(`Last error: ${state.lastError}`);
  if (state.lastTestResult) lines.push("", state.lastTestResult);
  return lines.join("\n");
}

export function formatGoalResumePrompt(state: GoalState): string {
  return [
    "Found unfinished Goal:",
    state.goal,
    `Attempt: ${state.attempt} / ${state.maxAttempts}`,
    "",
    "Run `/goal resume` to continue, or `/goal stop` to mark it failed.",
  ].join("\n");
}
