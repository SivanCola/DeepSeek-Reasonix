# Reasonix /goal 执行计划

生成日期：2026-05-29

## 目标

实现 `/goal <text>`，让 Reasonix 能围绕一个明确目标自动持续执行、调用工具、验证结果，并在完成时自动停止。

设计原则：

- Goal Loop：自动迭代直到目标完成或达到上限。
- Prefix Cache Friendly：Goal 和规则进入稳定 Prefix，动态结果只进入 Tail，尽量保持 DeepSeek Prefix Cache 命中。
- 极简实现：先做单 Agent、单 Loop、少状态，避免一开始引入多 Agent 架构。

## 当前代码落点

- CLI/TUI 斜杠命令：`src/cli/ui/slash/commands.ts`、`src/cli/ui/slash/dispatch.ts`、`src/cli/ui/slash/handlers/*`
- 主循环：`src/loop.ts` 的 `CacheFirstLoop.step()` 和 `CacheFirstLoop.run()`
- 稳定 Prefix：`src/memory/runtime.ts` 的 `ImmutablePrefix`
- 上下文压缩：`src/context-manager.ts` 的 `ContextManager.fold()`
- Code 模式入口：`src/cli/commands/code.tsx`
- 交互提交入口：`src/cli/ui/App.tsx`
- 配置：`src/config.ts`
- 测试框架：`vitest`

## 优先级

P0：

- `/goal`
- Goal Loop
- `GOAL_COMPLETED` 检测
- Max Attempts

P1：

- Prefix Cache Protection
- Auto Test
- Goal Recovery

P2：

- Context Compression
- Failure Memory

P3：

- 多 Agent，暂不做

## Phase 1 - MVP

### 1. Goal State 基础模型

新增模块：

- `src/goal/types.ts`
- `src/goal/state.ts`

核心类型：

```ts
export interface GoalState {
  goal: string;
  attempt: number;
  maxAttempts: number;
  status: "running" | "completed" | "failed";
  createdAt: string;
  updatedAt: string;
  lastError?: string;
  lastTestResult?: string;
  failedSolutions: string[];
  findings: GoalFinding[];
}

export interface GoalFinding {
  attempt: number;
  finding: string;
}
```

实现要求：

- 默认 `maxAttempts = 6`
- Goal 文本创建后不可自动改写
- 状态先内存可用，随后接入持久化

验收：

- 可以创建 GoalState
- attempt 能递增
- completed/failed/running 状态可判断

### 2. `/goal <text>` 斜杠命令

修改：

- `src/cli/ui/slash/commands.ts`
- `src/cli/ui/slash/dispatch.ts`
- 新增 `src/cli/ui/slash/handlers/goal.ts`

命令行为：

- `/goal 修复登录失败Bug`
- 创建 GoalState
- 进入 Goal Mode
- 自动提交一次 Goal Prompt 给模型
- 无参数时显示用法和当前 Goal 状态

建议扩展但不放入 MVP：

- `/goal status`
- `/goal stop`
- `/goal resume`

验收：

- `/help` 能看到 `/goal`
- `/goal <text>` 能触发一次模型调用
- 空 Goal 不进入 Goal Mode

### 3. Goal Prompt 模板

新增：

- `src/goal/prompt.ts`

模板职责：

- 注入 Goal
- 注入 Goal Rules
- 注入 Completion Rules
- 注入失败经验和最近测试结果

MVP 模板：

```text
GOAL:
{goal}

RULES:
1. 分析问题
2. 修改代码
3. 验证结果
4. 未完成继续执行
5. 完成时输出 GOAL_COMPLETED

COMPLETION RULES:
- 只有代码修改和验证都完成后，才输出 GOAL_COMPLETED
- 如果测试失败或仍有未解决问题，不要输出 GOAL_COMPLETED
- 完成时必须包含原因和验证结果
```

验收：

- Goal Prompt 可被单测快照验证
- Goal 文本只出现一次稳定注入

### 4. Goal Loop

新增：

- `src/goal/runner.ts`

优先采用极简封装：

```ts
for (attempt = 1; attempt <= maxAttempts; attempt++) {
  const result = await loop.run(nextGoalInput);
  if (isGoalCompleted(result)) break;
  nextGoalInput = buildGoalContinuation(state, result);
}
```

实现细节：

- 不改写 `CacheFirstLoop.step()` 的内部工具循环，Goal Loop 包在普通 turn 外层。
- 每轮结束后分析 assistant final 文本。
- 未完成时生成下一轮动态 Tail，例如最近错误、测试结果、失败方案。
- 到达 max attempts 后退出并输出失败摘要。

验收：

- Goal 最多运行 6 轮
- 中途检测到完成立即停止
- 未完成会继续下一轮

### 5. Completion Detection

新增：

- `src/goal/completion.ts`

规则：

- 检测 `GOAL_COMPLETED`
- 要求出现在 assistant final 文本中
- 初版只做字符串检测，避免引入复杂评分器

后续增强：

- 支持 JSON 结束块
- 检查测试结果是否通过
- 对误报增加防御

验收：

- `GOAL_COMPLETED` 能停止循环
- 非完成文本不会停止
- 大小写和 Markdown 包裹场景有测试

### 6. Max Attempts 配置

修改：

- `src/config.ts`
- CLI option 可后置，MVP 先支持 config/env

配置建议：

```json
{
  "goal": {
    "max_attempts": 6
  }
}
```

同时支持：

- `REASONIX_GOAL_MAX_ATTEMPTS=6`

验收：

- 默认 6
- 非法值回退默认
- 配置值能覆盖默认

## Phase 2 - Prefix Cache Friendly

### 1. Stable Goal Prefix

目标：

- Goal 创建后进入稳定 Prefix
- Goal 不参与每轮动态拼接
- 每轮只追加动态 Tail

推荐实现：

- 为 `ImmutablePrefix` 增加 Goal 扩展能力，或在创建 Goal Mode 时构造一次新的 stable system prompt。
- 避免每轮调用 `replaceSystem()`。
- Goal Mode 期间不重建完整 Prompt。

约束：

- Goal 文本不可被 Failure Memory、测试结果、最近错误污染
- 动态内容只能进入用户消息 Tail

验收：

- 同一个 Goal 的多轮请求中 `ImmutablePrefix.fingerprint` 保持不变
- 只有进入/退出 Goal Mode 时允许 prefix 变化

### 2. Prefix Cache Protection

实现要求：

- Goal Loop 不重建完整 Prompt
- 动态内容只追加为 user/assistant/tool messages
- tools 列表不在 Goal Loop 中变化
- 记录每轮 prefix hash，便于调试

建议事件：

- `goal_started`
- `goal_attempt_started`
- `goal_attempt_finished`
- `goal_prefix_hash`

验收：

- 单测覆盖多 attempt 下 prefix hash 不变
- 日志可看到每轮使用同一个 prefix hash

### 3. Context Compression

复用：

- `ContextManager.fold()`

Goal 专属压缩策略：

- 永远保留 Goal Prefix
- 保留最近一次失败原因
- 保留最近一次测试结果
- 保留 Failure Memory
- 压缩旧 attempt 详情

压缩摘要格式：

```text
Attempt 1-4:
已压缩

Summary:
- 修复 A 失败
- 修复 B 失败

Current:
- 正在验证修复 C
```

验收：

- 上下文过长时自动压缩
- 压缩后 Goal 不丢失
- 压缩后最近失败原因仍进入下一轮 Tail

## Phase 3 - 自动验证

### 1. Test Detection

新增：

- `src/goal/test-detection.ts`

检测规则：

- Node.js：`package.json`
- Python：`pytest.ini`、`pyproject.toml`、`setup.cfg`
- Go：`go.mod`
- Rust：`Cargo.toml`

命令优先级：

- Node：优先 `pnpm test`，其次 `npm test`
- Python：`pytest`
- Go：`go test ./...`
- Rust：`cargo test`

注意：

- 不在仓库无测试配置时强行运行
- 先只检测根目录，后续再支持 workspace/submodule

验收：

- 当前仓库检测为 Node.js
- 当前仓库建议命令为 `npm test`

### 2. Auto Test Runner

新增：

- `src/goal/auto-test.ts`

执行时机：

- 每轮模型声明已修改/准备验证时
- 或每轮结束后至少尝试一次轻量验证

初版策略：

- Goal attempt 完成后运行检测到的测试命令
- 超时默认 120 秒
- 捕获 stdout/stderr 摘要

验收：

- 测试通过时生成通过摘要
- 测试失败时写入 `lastTestResult`
- 命令不存在时不崩溃，返回 skipped

### 3. Verification Summary

输出格式：

```text
Tests:
✓ 125 passed
✗ 0 failed
```

实现要求：

- MVP 不强依赖解析所有框架输出
- 先保留原始命令、exit code、最后 N 行输出
- 能从 vitest/pytest/go test/cargo test 提取常见 passed/failed

验收：

- vitest 输出能生成简洁摘要
- 失败摘要进入下一轮 Goal Prompt Tail

## Phase 4 - Goal Recovery

### 1. Goal State 持久化

新增文件：

- 工作区根目录 `.reasonix-goal.json`

结构：

```json
{
  "goal": "修复登录失败Bug",
  "attempt": 3,
  "maxAttempts": 6,
  "status": "running",
  "lastError": "登录 token 校验失败",
  "lastTestResult": "npm test failed",
  "failedSolutions": [
    "只修改前端判断无效"
  ],
  "findings": [
    {
      "attempt": 2,
      "finding": "问题在 session cache 提前清理"
    }
  ]
}
```

实现要求：

- 原子写入，避免崩溃留下半文件
- Goal completed 后更新状态，不立即删除文件
- 提供清理命令可后续实现

验收：

- 每轮 attempt 后更新文件
- 进程重启后可读取未完成 Goal

### 2. Resume

启动时行为：

- 检查 `.reasonix-goal.json`
- 如果 `status=running`，提示用户是否继续
- MVP 可以先输出提示并要求用户输入 `/goal resume`
- P1 完成自动恢复交互

显示：

```text
发现未完成 Goal:
修复登录失败Bug
Attempt: 3 / 6
继续执行？
```

验收：

- 未完成 Goal 可恢复
- completed/failed 不自动恢复
- 损坏 JSON 有清晰错误并跳过

## Phase 5 - UX 优化

### 1. Goal Progress

每轮输出：

```text
Goal:
修复支付Bug

Attempt:
3 / 6

Status:
Testing

Files Changed:
- payment.ts
- payment.test.ts
```

实现落点：

- 新增 Goal 相关 `LoopEvent` 类型，或先使用 `info/status/warning`
- UI 初版可以走 `log.pushInfo`

验收：

- 用户能看到当前 attempt
- 用户能看到是否正在测试
- 用户能看到最终状态

### 2. Goal Summary

完成输出：

```text
Goal Completed

修改文件：
- payment.ts
- payment.test.ts

验证：
✓ npm test

结果：
支付回调重复执行问题已修复
```

验收：

- 完成时输出清晰摘要
- 达到 max attempts 时输出失败摘要和下一步建议

## Phase 6 - DeepSeek 专属增强

### 1. Failure Memory

目标：

- 记录最近失败方案
- 下一轮明确要求不要重复

状态字段：

```json
{
  "failed_solutions": [
    "方案A导致测试失败",
    "方案B导致死循环"
  ]
}
```

下一轮 Tail：

```text
不要重复以下方案：
1. 方案A导致测试失败
2. 方案B导致死循环
```

验收：

- 失败方案进入 `.reasonix-goal.json`
- 下一轮 prompt 包含失败方案
- 同一失败方案去重

### 2. Goal Reflection

每轮结束自动总结：

```text
What did I learn?
- 登录失败原因不是 Token，而是 Session Cache。
```

实现方式：

- MVP 可从 assistant final 中抽取 `Finding:`/`Learned:` 文本
- 后续可加一次低成本 summarization 调用

验收：

- findings 被持久化
- 下一轮 Tail 包含关键 finding

### 3. DeepSeek Reasoning Snapshot

目标：

- 保存本轮推理结论，不保存完整 CoT
- 降低 Token 和隐私风险
- 避免污染稳定 Prefix

结构：

```json
{
  "finding": "session key 被提前清理"
}
```

验收：

- 不持久化完整 reasoning_content
- 只保存短 finding
- finding 进入动态 Tail，不进入 Prefix

## 测试计划

单元测试：

- `src/goal/completion.ts`
- `src/goal/prompt.ts`
- `src/goal/state.ts`
- `src/goal/test-detection.ts`

集成测试：

- `/goal <text>` 能进入 Goal Mode
- 达到 `GOAL_COMPLETED` 后停止
- 未完成最多 6 轮
- Prefix hash 在同一 Goal 中保持不变
- `.reasonix-goal.json` 可恢复

手工验证：

```bash
npm test
npm run typecheck
npm run lint
```

当前仓库完整验证：

```bash
npm run verify
```

## MVP 验收标准

MVP 完成需要满足：

- `/goal xxx` 可用
- 自动循环执行
- 自动测试
- Goal 完成自动停止
- 最多 6 轮防死循环
- Goal 状态持久化
- Prefix Cache 不失效
- Context 自动压缩
- 失败经验避免重复尝试

## 推荐实施顺序

1. 新建 `src/goal/*`：types、state、prompt、completion。
2. 接入 `/goal` 斜杠命令，但先只触发一次 Goal Prompt。
3. 实现 `GoalRunner`，外层循环调用 `CacheFirstLoop.run()`。
4. 加入 Max Attempts 和 `GOAL_COMPLETED` 停止条件。
5. 将 Goal 固定进 stable prefix，并增加 prefix hash 测试。
6. 接入 `.reasonix-goal.json` 持久化。
7. 实现 test detection 和 auto test。
8. 接入 Failure Memory 和 Reflection。
9. 打磨 UI 输出和恢复流程。

## 暂不做

- 多 Agent
- 复杂评分器判定完成
- 自动 PR/commit
- 跨 workspace 的测试拓扑
- 复杂任务调度器

理由：

Reasonix 的差异化重点是 DeepSeek + Prefix Cache 省 Token。先把单 Agent Goal Loop 做稳，并保证 Prefix Cache Protection 和 Failure Memory，比过早引入多 Agent 更有价值。
