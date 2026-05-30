import { createHash } from "node:crypto";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { DeepSeekClient } from "../client.js";
import { CacheFirstLoop } from "../loop.js";
import { buildAssistantMessage } from "../loop/messages.js";
import { ImmutablePrefix } from "../memory/runtime.js";
import { appendSessionMessage } from "../memory/session.js";
import { countTokensBounded, estimateRequestTokens } from "../tokenizer.js";
import { ToolRegistry } from "../tools.js";
import type { ChatMessage, ToolCall, ToolSpec } from "../types.js";

const DEFAULT_MIN_HIT_RATIO = 0.85;
const MODEL = "deepseek-v4-flash";
const MESSAGE_CACHE_KEYS = [
  "role",
  "content",
  "name",
  "tool_call_id",
  "tool_calls",
  "reasoning_content",
] as const satisfies readonly (keyof ChatMessage)[];

export interface CacheGuardOptions {
  /** Minimum estimated cache-hit ratio for transitions that should be warm. */
  minHitRatio?: number;
  /** Keep the temporary HOME used for synthetic session files. */
  keepTemp?: boolean;
}

export interface CapturedCacheRequest {
  model: string;
  messages: ChatMessage[];
  tools: ToolSpec[];
  rendered: string;
  renderedTokens: number;
  requestTokens: number;
  immutablePrefixHash: string;
}

export interface CacheGuardTransition {
  from: number;
  to: number;
  estimatedHitRatio: number;
  estimatedMissTokens: number;
  immutablePrefixChanged: boolean;
  expectedBreak: boolean;
  passed: boolean;
  reason?: string;
}

export interface CacheGuardScenarioResult {
  name: string;
  description: string;
  requests: number;
  minEstimatedHitRatio: number | null;
  maxEstimatedMissTokens: number;
  expectedBreaks: number;
  transitions: CacheGuardTransition[];
  passed: boolean;
  error?: string;
}

export interface CacheGuardReport {
  passed: boolean;
  threshold: number;
  tempHome: string;
  scenarios: CacheGuardScenarioResult[];
}

interface ScriptedResponse {
  content: string;
  reasoningContent?: string | null;
  toolCalls?: ToolCall[];
  completionTokens?: number;
}

interface ScenarioRun {
  name: string;
  description: string;
  driver: ScriptedDeepSeek;
  expectedBreaks: Set<number>;
}

class ScriptedDeepSeek {
  readonly requests: CapturedCacheRequest[] = [];
  private readonly responses: ScriptedResponse[] = [];

  enqueue(...responses: ScriptedResponse[]): void {
    this.responses.push(...responses);
  }

  readonly fetch: typeof fetch = async (_url, init) => {
    const payload = parsePayload(init?.body);
    const request = capturePayload(payload);
    const previous = this.requests[this.requests.length - 1];
    this.requests.push(request);

    const next = this.responses.shift();
    if (!next) {
      return new Response("cache guard has no scripted response left", { status: 500 });
    }

    const hitRatio = previous ? estimateRenderedHitRatio(previous.rendered, request.rendered) : 0;
    const promptTokens = request.requestTokens;
    const promptCacheHitTokens = Math.floor(promptTokens * hitRatio);
    const promptCacheMissTokens = Math.max(0, promptTokens - promptCacheHitTokens);
    const completionTokens = next.completionTokens ?? countTokensBounded(next.content || "ok");

    return new Response(
      JSON.stringify({
        choices: [
          {
            index: 0,
            message: {
              role: "assistant",
              content: next.content,
              reasoning_content: next.reasoningContent ?? "",
              ...(next.toolCalls && next.toolCalls.length > 0
                ? { tool_calls: next.toolCalls }
                : {}),
            },
            finish_reason: next.toolCalls && next.toolCalls.length > 0 ? "tool_calls" : "stop",
          },
        ],
        usage: {
          prompt_tokens: promptTokens,
          completion_tokens: completionTokens,
          total_tokens: promptTokens + completionTokens,
          prompt_cache_hit_tokens: promptCacheHitTokens,
          prompt_cache_miss_tokens: promptCacheMissTokens,
        },
      }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    );
  };
}

export async function runCacheGuard(opts: CacheGuardOptions = {}): Promise<CacheGuardReport> {
  const threshold = opts.minHitRatio ?? DEFAULT_MIN_HIT_RATIO;
  const tempHome = mkdtempSync(join(tmpdir(), "reasonix-cache-guard-"));
  const previousHome = process.env.HOME;
  const previousUserProfile = process.env.USERPROFILE;
  process.env.HOME = tempHome;
  process.env.USERPROFILE = tempHome;

  try {
    const scenarioRuns = await Promise.all([
      runPlainDialogueScenario(),
      runToolRoundTripScenario(),
      runMultiToolScenario(),
      runReasoningRetentionScenario(),
      runLongSessionResumeScenario(),
      runMcpHotAddScenario(),
      runProOneShotScenario(),
    ]);
    const scenarios = scenarioRuns.map((run) =>
      analyzeScenario(
        run.name,
        run.description,
        run.driver.requests,
        run.expectedBreaks,
        threshold,
      ),
    );
    return {
      passed: scenarios.every((scenario) => scenario.passed),
      threshold,
      tempHome,
      scenarios,
    };
  } finally {
    if (previousHome === undefined) Reflect.deleteProperty(process.env, "HOME");
    else process.env.HOME = previousHome;
    if (previousUserProfile === undefined) Reflect.deleteProperty(process.env, "USERPROFILE");
    else process.env.USERPROFILE = previousUserProfile;
    if (!opts.keepTemp) rmSync(tempHome, { recursive: true, force: true });
  }
}

export function renderCacheGuardReport(report: CacheGuardReport): string {
  const lines = [
    `cache guard: ${report.passed ? "PASS" : "FAIL"}  threshold=${pct(report.threshold)}`,
    "",
    "scenario                         req  min-hit  max-miss  breaks  result",
  ];
  for (const scenario of report.scenarios) {
    const minHit =
      scenario.minEstimatedHitRatio === null ? "n/a" : pct(scenario.minEstimatedHitRatio);
    lines.push(
      `${pad(scenario.name, 32)} ${String(scenario.requests).padStart(3)}  ${minHit.padStart(7)}  ${String(
        scenario.maxEstimatedMissTokens,
      ).padStart(8)}  ${String(scenario.expectedBreaks).padStart(6)}  ${
        scenario.passed ? "PASS" : "FAIL"
      }`,
    );
    for (const transition of scenario.transitions) {
      if (transition.passed) continue;
      lines.push(
        `  - ${transition.from}->${transition.to}: hit=${pct(
          transition.estimatedHitRatio,
        )}, miss~${transition.estimatedMissTokens}, ${transition.reason ?? "cache regression"}`,
      );
    }
    if (scenario.error) lines.push(`  - error: ${scenario.error}`);
  }
  return lines.join("\n");
}

export function analyzeScenario(
  name: string,
  description: string,
  requests: readonly CapturedCacheRequest[],
  expectedBreaks: ReadonlySet<number>,
  threshold: number,
): CacheGuardScenarioResult {
  const transitions: CacheGuardTransition[] = [];
  for (let i = 1; i < requests.length; i++) {
    const previous = requests[i - 1]!;
    const current = requests[i]!;
    const expectedBreak = expectedBreaks.has(i);
    const estimatedHitRatio = estimateRenderedHitRatio(previous.rendered, current.rendered);
    const estimatedMissTokens = Math.max(
      0,
      Math.round(current.requestTokens * (1 - estimatedHitRatio)),
    );
    const immutablePrefixChanged = previous.immutablePrefixHash !== current.immutablePrefixHash;
    const passed = expectedBreak || (!immutablePrefixChanged && estimatedHitRatio >= threshold);
    const reason = passed
      ? undefined
      : immutablePrefixChanged
        ? "immutable prefix changed unexpectedly"
        : "estimated cache-hit ratio below threshold";
    transitions.push({
      from: i - 1,
      to: i,
      estimatedHitRatio,
      estimatedMissTokens,
      immutablePrefixChanged,
      expectedBreak,
      passed,
      reason,
    });
  }
  const warmTransitions = transitions.filter((transition) => !transition.expectedBreak);
  const minEstimatedHitRatio =
    warmTransitions.length === 0
      ? null
      : Math.min(...warmTransitions.map((transition) => transition.estimatedHitRatio));
  const maxEstimatedMissTokens =
    warmTransitions.length === 0
      ? 0
      : Math.max(...warmTransitions.map((transition) => transition.estimatedMissTokens));
  return {
    name,
    description,
    requests: requests.length,
    minEstimatedHitRatio,
    maxEstimatedMissTokens,
    expectedBreaks: transitions.filter((transition) => transition.expectedBreak).length,
    transitions,
    passed: transitions.every((transition) => transition.passed),
  };
}

function makeLoop(driver: ScriptedDeepSeek, opts: { session?: string } = {}): CacheFirstLoop {
  const tools = makeGuardTools();
  const prefix = new ImmutablePrefix({
    system: stableSystemPrompt(),
    toolSpecs: tools.specs(),
  });
  const client = new DeepSeekClient({
    apiKey: "sk-cache-guard",
    fetch: driver.fetch,
    retry: { maxAttempts: 1 },
  });
  return new CacheFirstLoop({
    client,
    prefix,
    tools,
    stream: false,
    model: MODEL,
    maxIterPerTurn: 8,
    session: opts.session,
  });
}

async function runPlainDialogueScenario(): Promise<ScenarioRun> {
  const driver = new ScriptedDeepSeek();
  driver.enqueue(
    { content: "I inspected the request shape." },
    { content: "The follow-up keeps the prefix stable." },
    { content: "The final answer still reuses the warmed prefix." },
  );
  const loop = makeLoop(driver);
  await runUser(loop, "Explain the current cache strategy in one paragraph.");
  await runUser(loop, "Now summarize the risk if the system prompt changes.");
  await runUser(loop, "Close with the mitigation.");
  return {
    name: "plain-dialogue",
    description: "普通多轮问答，覆盖 assistant 历史追加后的 warm prefix。",
    driver,
    expectedBreaks: new Set(),
  };
}

async function runToolRoundTripScenario(): Promise<ScenarioRun> {
  const driver = new ScriptedDeepSeek();
  driver.enqueue(
    {
      content: "",
      reasoningContent: "Need to inspect a file before answering.",
      toolCalls: [toolCall("read_file", { path: "src/loop.ts" }, "call_read_loop")],
    },
    { content: "The file read result was incorporated without changing the immutable prefix." },
    { content: "A second user turn stays warm after the tool result is in history." },
  );
  const loop = makeLoop(driver);
  await runUser(loop, "Read the loop implementation and explain where messages are built.");
  await runUser(loop, "What cache-sensitive invariant should we keep?");
  return {
    name: "tool-roundtrip",
    description: "单工具调用 + tool result 上传 + 下一轮用户追问。",
    driver,
    expectedBreaks: new Set(),
  };
}

async function runMultiToolScenario(): Promise<ScenarioRun> {
  const driver = new ScriptedDeepSeek();
  driver.enqueue(
    {
      content: "",
      reasoningContent: "Search first, then inspect the matching file.",
      toolCalls: [
        toolCall("search_content", { query: "healActiveLogBeforeSend" }, "call_search"),
        toolCall("list_directory", { path: "src/loop" }, "call_list"),
      ],
    },
    { content: "Both tool results were consumed in one continuation." },
    { content: "The next turn remains cache-friendly." },
  );
  const loop = makeLoop(driver);
  await runUser(loop, "Find the cache-sensitive healing paths.");
  await runUser(loop, "Now give the short conclusion.");
  return {
    name: "multi-tool",
    description: "同一轮多个 tool_calls，覆盖并行/批量结果进入下一次 prompt。",
    driver,
    expectedBreaks: new Set(),
  };
}

async function runReasoningRetentionScenario(): Promise<ScenarioRun> {
  const driver = new ScriptedDeepSeek();
  driver.enqueue(
    { content: "Reasoning-bearing answer.", reasoningContent: "private reasoning block" },
    { content: "Follow-up after reasoning retention pruning." },
    { content: "Another follow-up confirms the warmed prefix." },
  );
  const loop = makeLoop(driver);
  await runUser(loop, "Answer with thinking-mode metadata present.");
  await runUser(loop, "Ask a follow-up after the assistant reasoning is historical.");
  await runUser(loop, "Ask one more follow-up.");
  return {
    name: "reasoning-retention",
    description: "thinking-mode assistant reasoning round-trip / prune 路径。",
    driver,
    expectedBreaks: new Set(),
  };
}

async function runLongSessionResumeScenario(): Promise<ScenarioRun> {
  const session = "cache-guard-long-session";
  for (let i = 0; i < 130; i++) {
    appendSessionMessage(session, {
      role: "user",
      content: `historical user turn ${i}: ${longStableLine(i)}`,
    });
    appendSessionMessage(
      session,
      buildAssistantMessage(`historical assistant turn ${i}: ${longStableLine(i)}`, [], MODEL, ""),
    );
  }
  const driver = new ScriptedDeepSeek();
  driver.enqueue(
    { content: "Resumed long session successfully." },
    { content: "Follow-up after resume stayed warm." },
  );
  const loop = makeLoop(driver, { session });
  await runUser(loop, "Resume this long session and answer briefly.");
  await runUser(loop, "One more follow-up in the same long session.");
  return {
    name: "long-session-resume",
    description: "超过 200 条消息的窗口日志恢复，覆盖 toFullHistory()/session fallback。",
    driver,
    expectedBreaks: new Set(),
  };
}

async function runMcpHotAddScenario(): Promise<ScenarioRun> {
  const driver = new ScriptedDeepSeek();
  driver.enqueue(
    { content: "Before MCP hot-add." },
    { content: "After MCP hot-add; one cache break is expected." },
    { content: "The following turn should be warm again." },
  );
  const expectedBreaks = new Set<number>();
  const loop = makeLoop(driver);
  await runUser(loop, "Start before adding an MCP-like tool.");
  expectedBreaks.add(driver.requests.length);
  const mcpSpec = makeMcpToolSpec();
  loop.tools.register({
    name: mcpSpec.function.name,
    description: mcpSpec.function.description,
    parameters: mcpSpec.function.parameters,
    readOnly: true,
    fn: () => "mcp result",
  });
  loop.prefix.addTool(mcpSpec);
  await runUser(loop, "Now continue after the MCP tool was hot-added.");
  await runUser(loop, "Verify the re-warmed prefix.");
  return {
    name: "mcp-hot-add",
    description: "MCP 工具热添加允许一次预期 cache break，随后必须恢复高命中。",
    driver,
    expectedBreaks,
  };
}

async function runProOneShotScenario(): Promise<ScenarioRun> {
  const driver = new ScriptedDeepSeek();
  driver.enqueue(
    { content: "<<<NEEDS_PRO: cache guard escalation check>>>" },
    { content: "Pro one-shot answer." },
    { content: "Back on flash after one-shot escalation." },
    { content: "A stable flash follow-up." },
  );
  const loop = makeLoop(driver);
  await runUser(loop, "Use the pro one-shot path if needed.");
  const expectedBreaks = new Set([1, 2]);
  await runUser(loop, "Confirm the model restored to flash.");
  await runUser(loop, "Confirm the next flash turn is warm.");
  return {
    name: "pro-one-shot",
    description: "Flash -> Pro one-shot escalation 是预期 break，恢复后下一轮必须 warm。",
    driver,
    expectedBreaks,
  };
}

async function runUser(loop: CacheFirstLoop, prompt: string): Promise<void> {
  const errors: string[] = [];
  await loop.run(prompt, (event) => {
    if (event.role === "error") errors.push(event.error ?? "unknown loop error");
  });
  if (errors.length > 0) throw new Error(errors.join("; "));
}

function makeGuardTools(): ToolRegistry {
  const tools = new ToolRegistry();
  tools.register({
    name: "read_file",
    description:
      "Read a UTF-8 text file from the project and return a bounded excerpt with path, line count, and relevant content.",
    readOnly: true,
    parameters: {
      type: "object",
      properties: { path: { type: "string", description: "Project-relative file path." } },
      required: ["path"],
    },
    fn: (args: { path: string }) =>
      `file ${args.path}\n1 export function healActiveLogBeforeSend() {}\n2 // stable cache guard fixture`,
  });
  tools.register({
    name: "search_content",
    description:
      "Search project text with a literal query and return matching file paths with compact snippets.",
    readOnly: true,
    parameters: {
      type: "object",
      properties: { query: { type: "string", description: "Literal query to search." } },
      required: ["query"],
    },
    fn: (args: { query: string }) =>
      JSON.stringify({ query: args.query, matches: ["src/loop.ts:555", "src/tokenizer.ts:612"] }),
  });
  tools.register({
    name: "list_directory",
    description:
      "List a project directory with stable ordering, including file names, child counts, and short type labels.",
    readOnly: true,
    parameters: {
      type: "object",
      properties: { path: { type: "string", description: "Project-relative directory path." } },
      required: ["path"],
    },
    fn: (args: { path: string }) =>
      JSON.stringify({ path: args.path, entries: ["healing.ts", "messages.ts", "streaming.ts"] }),
  });
  tools.register({
    name: "edit_file",
    description:
      "Apply a SEARCH/REPLACE edit block to one file. Included in the guard to keep mutating tool schemas in the prefix.",
    parameters: {
      type: "object",
      properties: {
        path: { type: "string" },
        search: { type: "string" },
        replace: { type: "string" },
      },
      required: ["path", "search", "replace"],
    },
    fn: () => "edit accepted by cache guard fixture",
  });
  tools.register({
    name: "todo_write",
    description:
      "Write the current task checklist with pending, in_progress, and completed states.",
    parameters: {
      type: "object",
      properties: {
        todos: {
          type: "array",
          items: {
            type: "object",
            properties: {
              content: { type: "string" },
              status: { type: "string", enum: ["pending", "in_progress", "completed"] },
            },
            required: ["content", "status"],
          },
        },
      },
      required: ["todos"],
    },
    fn: () => "todos saved",
  });
  return tools;
}

function capturePayload(payload: {
  model?: string;
  messages?: ChatMessage[];
  tools?: ToolSpec[];
}): CapturedCacheRequest {
  const model = payload.model ?? MODEL;
  const messages = payload.messages ?? [];
  const tools = payload.tools ?? [];
  const rendered = renderCacheGuardSurface({ model, messages, tools });
  return {
    model,
    messages,
    tools,
    rendered,
    renderedTokens: countTokensBounded(rendered),
    requestTokens: estimateRequestTokens(messages, tools, true),
    immutablePrefixHash: hashImmutablePrefix(model, messages, tools),
  };
}

export function renderCacheGuardSurface(req: {
  model: string;
  messages: ChatMessage[];
  tools: ToolSpec[];
}): string {
  const systemMessages = req.messages
    .filter((message) => message.role === "system")
    .map(normalizeMessageForCache);
  const conversationMessages = req.messages
    .filter((message) => message.role !== "system")
    .map(normalizeMessageForCache);
  return [
    `model:${req.model}`,
    `tools:${stableStringify(req.tools)}`,
    `system:${stableStringify(systemMessages)}`,
    `conversation:${stableStringify(conversationMessages)}`,
  ].join("\n");
}

function hashImmutablePrefix(model: string, messages: ChatMessage[], tools: ToolSpec[]): string {
  const system = messages.find((message) => message.role === "system")?.content ?? "";
  return hash({ model, system, tools });
}

function estimateRenderedHitRatio(previous: string, current: string): number {
  const shared = commonPrefixLength(previous, current);
  if (current.length === 0) return 1;
  return Math.min(1, countTokensBounded(current.slice(0, shared)) / countTokensBounded(current));
}

function commonPrefixLength(a: string, b: string): number {
  const max = Math.min(a.length, b.length);
  let i = 0;
  while (i < max && a.charCodeAt(i) === b.charCodeAt(i)) i++;
  return i;
}

function normalizeMessageForCache(message: ChatMessage): Record<string, unknown> {
  const normalized: Record<string, unknown> = {};
  for (const key of MESSAGE_CACHE_KEYS) {
    const value = message[key];
    if (value !== undefined) normalized[key] = value;
  }
  return normalized;
}

function stableStringify(value: unknown): string {
  return JSON.stringify(sortJson(value));
}

function sortJson(value: unknown): unknown {
  if (Array.isArray(value)) return value.map((item) => sortJson(item));
  if (value !== null && typeof value === "object") {
    const input = value as Record<string, unknown>;
    const sorted: Record<string, unknown> = {};
    for (const key of Object.keys(input).sort()) {
      const child = input[key];
      if (child !== undefined) sorted[key] = sortJson(child);
    }
    return sorted;
  }
  return value;
}

function parsePayload(body: unknown): {
  model?: string;
  messages?: ChatMessage[];
  tools?: ToolSpec[];
} {
  if (typeof body === "string") return JSON.parse(body);
  if (body instanceof Uint8Array) return JSON.parse(new TextDecoder().decode(body));
  return {};
}

function stableSystemPrompt(): string {
  const lines = Array.from(
    { length: 220 },
    (_, i) =>
      `Cache invariant ${String(i + 1).padStart(2, "0")}: preserve stable system text, tool schema order, message history, reasoning retention, MCP append semantics, and long-session recovery.`,
  );
  return [
    "You are the Reasonix cache guard fixture. This prompt is intentionally large and deterministic.",
    ...lines,
  ].join("\n");
}

function longStableLine(i: number): string {
  return `cache-safe historical payload ${i} ${"alpha beta gamma delta ".repeat(8)}`;
}

function toolCall(name: string, args: Record<string, unknown>, id: string): ToolCall {
  return {
    id,
    type: "function",
    function: { name, arguments: JSON.stringify(args) },
  };
}

function makeMcpToolSpec(): ToolSpec {
  return {
    type: "function",
    function: {
      name: "exa_search",
      description:
        "MCP-provided semantic web search tool. Hot-added in the cache guard to model an expected one-turn cache break.",
      parameters: {
        type: "object",
        properties: {
          query: { type: "string" },
          domains: { type: "array", items: { type: "string" } },
        },
        required: ["query"],
      },
    },
  };
}

function hash(value: unknown): string {
  return createHash("sha256").update(JSON.stringify(value)).digest("hex").slice(0, 16);
}

function pct(value: number): string {
  return `${(value * 100).toFixed(1)}%`;
}

function pad(value: string, width: number): string {
  return value.length >= width ? value : value + " ".repeat(width - value.length);
}
