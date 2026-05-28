import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DeepSeekClient } from "../src/client.js";
import { loadGoalMaxAttempts } from "../src/config.js";
import { isGoalCompleted } from "../src/goal/completion.js";
import {
  buildGoalContinuationInput,
  buildInitialGoalInput,
  buildStableGoalPrefix,
  removeStableGoalPrefix,
  withStableGoalPrefix,
} from "../src/goal/prompt.js";
import { runGoalLoop } from "../src/goal/runner.js";
import { createGoalState, goalStatePath, loadGoalState, saveGoalState } from "../src/goal/state.js";
import { detectTestCommand } from "../src/goal/test-detection.js";
import { CacheFirstLoop } from "../src/loop.js";
import { ImmutablePrefix } from "../src/memory/runtime.js";

function makeLoop(system = "base system") {
  const client = new DeepSeekClient({
    apiKey: "sk-test",
    fetch: vi.fn() as unknown as typeof fetch,
  });
  return new CacheFirstLoop({
    client,
    prefix: new ImmutablePrefix({ system }),
  });
}

describe("goal mode", () => {
  let dir: string;

  beforeEach(() => {
    dir = mkdtempSync(join(tmpdir(), "reasonix-goal-"));
  });

  afterEach(() => {
    rmSync(dir, { recursive: true, force: true });
    process.env.REASONIX_GOAL_MAX_ATTEMPTS = "";
  });

  it("detects GOAL_COMPLETED case-insensitively", () => {
    expect(isGoalCompleted("GOAL_COMPLETED\nreason: done")).toBe(true);
    expect(isGoalCompleted("goal_completed in markdown")).toBe(true);
    expect(isGoalCompleted("still working")).toBe(false);
  });

  it("keeps goal text in stable prefix and out of dynamic inputs", () => {
    const goal = "Fix login failure";
    const state = createGoalState(goal);
    state.attempt = 1;
    const stable = buildStableGoalPrefix(goal);
    expect(stable.match(new RegExp(goal, "g"))).toHaveLength(1);
    expect(buildInitialGoalInput(state)).not.toContain(goal);
    expect(buildGoalContinuationInput(state, "still working")).not.toContain(goal);
  });

  it("round-trips goal state through .reasonix-goal.json", () => {
    const state = createGoalState("Fix login failure", {
      maxAttempts: 3,
      now: new Date("2026-05-29T00:00:00.000Z"),
    });
    saveGoalState(dir, state);
    expect(existsSync(goalStatePath(dir))).toBe(true);
    const loaded = loadGoalState(dir);
    expect(loaded.error).toBeUndefined();
    expect(loaded.state?.goal).toBe("Fix login failure");
    expect(loaded.state?.maxAttempts).toBe(3);
  });

  it("loads goal max attempts from env and config with invalid fallback", () => {
    expect(loadGoalMaxAttempts(join(dir, "missing.json"))).toBe(6);
    process.env.REASONIX_GOAL_MAX_ATTEMPTS = "4";
    expect(loadGoalMaxAttempts(join(dir, "missing.json"))).toBe(4);
    process.env.REASONIX_GOAL_MAX_ATTEMPTS = "";
    const cfg = join(dir, "config.json");
    writeFileSync(cfg, JSON.stringify({ goal: { max_attempts: 5 } }), "utf8");
    expect(loadGoalMaxAttempts(cfg)).toBe(5);
    writeFileSync(cfg, JSON.stringify({ goal: { max_attempts: -1 } }), "utf8");
    expect(loadGoalMaxAttempts(cfg)).toBe(6);
  });

  it("detects node test commands with npm as the default for this repo shape", () => {
    writeFileSync(
      join(dir, "package.json"),
      JSON.stringify({ scripts: { test: "vitest run" } }),
      "utf8",
    );
    writeFileSync(join(dir, "package-lock.json"), "{}", "utf8");
    expect(detectTestCommand(dir)?.display).toBe("npm test");
    writeFileSync(join(dir, "pnpm-lock.yaml"), "", "utf8");
    expect(detectTestCommand(dir)?.display).toBe("pnpm test");
  });

  it("runs attempts until GOAL_COMPLETED and keeps the goal prefix hash stable", async () => {
    const loop = makeLoop();
    const baseHash = loop.prefix.fingerprint;
    const state = createGoalState("Fix login failure", { maxAttempts: 3 });
    const prefixHashes: string[] = [];
    const responses = ["Still investigating", "GOAL_COMPLETED\nReason: tests passed"];

    const result = await runGoalLoop({
      rootDir: dir,
      loop,
      state,
      runTests: false,
      onInfo: () => {},
      runTurn: async () => {
        prefixHashes.push(loop.prefix.fingerprint);
        const content = responses.shift() ?? "GOAL_COMPLETED";
        loop.appendAndPersist({ role: "assistant", content });
        return content;
      },
    });

    expect(result.completed).toBe(true);
    expect(result.state.attempt).toBe(2);
    expect(new Set(prefixHashes).size).toBe(1);
    expect(loop.prefix.fingerprint).toBe(baseHash);
    expect(loadGoalState(dir).state?.status).toBe("completed");
  });

  it("fails after max attempts without GOAL_COMPLETED", async () => {
    const loop = makeLoop();
    const result = await runGoalLoop({
      rootDir: dir,
      loop,
      state: createGoalState("Fix login failure", { maxAttempts: 2 }),
      runTests: false,
      onInfo: () => {},
      runTurn: async () => "not done",
    });

    expect(result.completed).toBe(false);
    expect(result.state.status).toBe("failed");
    expect(result.state.attempt).toBe(2);
    expect(readFileSync(goalStatePath(dir), "utf8")).toContain("Reached max attempts");
  });

  it("can remove a stable goal prefix from the system prompt", () => {
    const withGoal = withStableGoalPrefix("base system", "Fix login failure");
    expect(withGoal).toContain("GOAL:");
    expect(removeStableGoalPrefix(withGoal)).toBe("base system");
  });
});
