import { describe, expect, it } from "vitest";
import {
  type CapturedCacheRequest,
  analyzeScenario,
  renderCacheGuardSurface,
  runCacheGuard,
} from "../src/telemetry/cache-guard.js";
import type { ChatMessage, ToolSpec } from "../src/types.js";

describe("cache guard", () => {
  it("passes the built-in cache-sensitive dialogue scenarios", async () => {
    const report = await runCacheGuard();

    expect(report.passed).toBe(true);
    expect(report.scenarios.map((scenario) => scenario.name)).toEqual([
      "plain-dialogue",
      "tool-roundtrip",
      "multi-tool",
      "reasoning-retention",
      "long-session-resume",
      "mcp-hot-add",
      "pro-one-shot",
    ]);
    for (const scenario of report.scenarios) {
      expect(scenario.passed, scenario.name).toBe(true);
      expect(scenario.requests, scenario.name).toBeGreaterThan(1);
    }
  });

  it("fails an unexpected immutable-prefix change", () => {
    const base = {
      model: "deepseek-v4-flash",
      messages: [],
      tools: [],
      rendered: "model:deepseek-v4-flash\ntools:[]\nprompt:system-a user-a",
      renderedTokens: 100,
      requestTokens: 100,
      immutablePrefixHash: "same",
    };
    const changed = {
      ...base,
      rendered: 'model:deepseek-v4-flash\ntools:[{"name":"new"}]\nprompt:system-a user-a',
      immutablePrefixHash: "changed",
    };

    const result = analyzeScenario("fixture", "fixture", [base, changed], new Set(), 0.9);

    expect(result.passed).toBe(false);
    expect(result.transitions[0]?.reason).toBe("immutable prefix changed unexpectedly");
  });

  it("fails when historical thinking-mode assistant shape loses empty reasoning_content", () => {
    const tools: ToolSpec[] = [];
    const system: ChatMessage = {
      role: "system",
      content: "stable cache prefix ".repeat(800),
    };
    const history: ChatMessage[] = Array.from({ length: 12 }, (_, i) => [
      { role: "user" as const, content: `question ${i}` },
      { role: "assistant" as const, content: `answer ${i}`, reasoning_content: "" },
    ]).flat();
    const currentHistory = history.map((message, i) =>
      i === 1 ? { role: "assistant" as const, content: message.content } : message,
    );

    const previous = cacheRequest([system, ...history], tools);
    const current = cacheRequest(
      [...currentHistory, { role: "user", content: "next question" }],
      tools,
    );

    expect(previous.rendered).toContain('"reasoning_content":""');
    const result = analyzeScenario("fixture", "fixture", [previous, current], new Set(), 0.85);

    expect(result.passed).toBe(false);
    expect(result.transitions[0]?.reason).toBe("estimated cache-hit ratio below threshold");
  });
});

function cacheRequest(messages: ChatMessage[], tools: ToolSpec[]): CapturedCacheRequest {
  const rendered = renderCacheGuardSurface({
    model: "deepseek-v4-flash",
    messages,
    tools,
  });
  return {
    model: "deepseek-v4-flash",
    messages,
    tools,
    rendered,
    renderedTokens: 100,
    requestTokens: 100,
    immutablePrefixHash: "same",
  };
}
