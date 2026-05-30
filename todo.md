# Reasonix Todo

> 基于 Pi Agent 设计理念对照当前代码状态的改进清单。
> 优先级：P0 > P1 > P2 > P3

---

## 架构原则

本项目最大的设计约束是 **DeepSeek 的 prompt-cache 定价结构**：

| | cache_hit (¥/M tokens) | input (¥/M tokens) | 价差 |
|---|---|---|---|
| deepseek-v4-flash | 0.02 | 1 | **50x** |
| deepseek-v4-pro | 0.025 | 3 | **120x** |

所有新功能必须遵守以下缓存纪律：

- **prepend-only**: session 消息只能追加，绝不修改已有内容（任一修改 → cache break）
- **prefix stability**: system prompt、tool schemas 保持稳定；任何变更必须通过 `PrefixShape` 显式追踪
- **reasoning pruning**: 纯文本回复的 `reasoning_content` 必须在下一轮前剥离（`stripStaleReasoning`），仅 tool-call 轮次保留
- **compaction economics**: `foldEconomics` 核算通过后才触发压缩，不盲目压缩

以下每个待办项标注了缓存影响评估：🟢 无影响 / 🟡 可控 / 🔴 需谨慎设计。

---

## P0 — 核心架构完善

### Session Tree（树形会话）🟢

**Pi Agent 启发**: 会话以树组织，天然支持分支、回退、多路径探索。

**当前状态**: `internal/agent/session_tree.go` 已有完整数据结构 + `Branch`/`SwitchTo`/`Save`/`Load`，CLI 已集成 `/branch`、`/tree`、`/switch` 命令。树结构仅在内存中——关闭进程后分支信息丢失。

**缓存分析**: 每个分支是独立的 `[]Message` 序列，共享祖先 prefix 天然可复用缓存。分支间互不污染——与当前 prepend-only 设计完全兼容。

**待做**:
- [ ] `Session.Merge()` — 合并策略（取最优结果 / 用户选择），合并结果作为新节点追加
- [ ] **树持久化**：退出时自动保存树到 `session dir`，启动 `reasonix chat --resume` 时恢复完整树结构
- [ ] 树保存与线性 session 文件的协同——避免 schema 冲突

### Goal 系统集成 🟡

**当前状态**: `internal/agent/goal/` 已有 `goal.go` 和 `auto_test.go`，但与 Agent 主循环的集成不明确。

**缓存分析**: Goal 描述应注入为 system prompt 的一部分（prepend-only 前缀）。目标完成度状态不应频繁写回消息历史（破坏 cache prefix）。

**待做**:
- [ ] Goal 作为 Coordinator Planner 阶段的前置步骤：Goal 拆解 → Planner 制定执行计划 → Executor 执行
- [ ] Goal 进度追踪放在 `Session.metadata` 中（不进入 Messages，不破坏缓存）
- [ ] 重规划触发条件：仅当 goal 状态变更时才产生新计划，追加而非修改

### Context Compression 语义化 🔴

**Pi Agent 启发**: 压缩不是截断，是保留语义、丢掉冗余。

**当前状态**: `internal/agent/compact.go` 已有 LLM 摘要 + `foldEconomics` 经济核算。压缩替换中间消息 → bump `rewriteVersion` → cache break。

**缓存分析**: 这是 todo.md 中对缓存最敏感的项。当前策略是"压缩 = 一次有意的 cache break + 后续更小的 prompt 回暖"。理想的增量压缩（修改中间但 prefix 不变）在 DeepSeek API 语义下没有明确保证。

**待做**:
- [ ] 结构化摘要格式：按 topic/task/decision 分段，而非一段自由文本
- [ ] `compact_msg` role 或专门的 summary 标记，与普通 user message 区分（方便调试和回放）
- [ ] 保留策略配置：`[compact] keep = ["errors", "user_marked"]`
- [ ] 压缩后 sanity check：用一次廉价调用验证关键信息未丢失
- [ ] **增量压缩（实验性）**: 仅在尾部窗口内做替换、保持前缀 N 条消息不动，然后实测缓存命中率变化。若 API 不支持则回退到当前全量 break 策略

---

## P1 — 现有能力强化

### 缓存可观测性 🟢 [新增]

**当前状态**: `cache_shape.go` 已有 `PrefixShape` + `CacheDiagnostics`，`agent.go` 每 turn 记录诊断数据到 `diagHistory`（保留最近 50 条）。

**待做**:
- [ ] `/cache-report` 命令：展示最近 N 轮的缓存命中率、prefix 变化原因、tool schema 开销
- [ ] 缓存预警：当 `CacheMissTokens` 异常飙升时 emit Notice 事件
- [ ] Tool schema token 开销排名（`SchemaTokenCosts`）：识别哪些工具最占缓存空间
- [ ] Dashboard 缓存面板：命中率趋势图、prefix break 时间线、成本节省估算

### Agent Loop 可测试性 🟢

**Pi Agent 启发**: mock LLM、验证 loop 决策而非真实调用。

**当前状态**: `cache_guard_test.go` 已有 `cacheGuardProvider` 雏形——按 turn 脚本控制返回值，记录所有请求用于离线分析。

**缓存分析**: 无关。但测试框架本身应验证缓存行为不退化。

**待做**:
- [ ] `MockProvider` 从 `cacheGuardProvider` 抽象，支持更多场景（错误注入、延迟模拟）
- [ ] Agent loop 单元测试：给定 message 序列，验证 tool dispatch 逻辑、compact 触发条件、maxSteps 终止
- [ ] 事件流验证：Run 后检查 emit 的事件种类、顺序和内容
- [ ] **缓存回归测试**：扩展 `TestCacheGuard_All`，确保每次改动后命中率不低于 85% 阈值

### Provider 层抽象 🟡

**Pi Agent 启发**: pi-ai / pi-agent-core / pi-coding-agent 三层分离。

**当前状态**: `internal/provider/provider.go` 接口已存在，OpenAI 实现独立。但 `agent.go` 中有 LLM 特定行为（`reasoning_content` 剥离）。

**缓存分析**: `stripStaleReasoning` 是缓存优化但属于 provider 特定逻辑——Anthropic 不需要。下沉到 provider 时应保持 agent 层的缓存契约不变。

**待做**:
- [ ] `Provider` 接口增加 `PostProcessMessage(m Message) Message` 可选方法，agent 在 `session.Add` 前调用
- [ ] `stripStaleReasoning` 下沉为 OpenAI provider 的 `PostProcessMessage` 实现
- [ ] 排查 `agent.go` 中其他与 provider 耦合的逻辑，一并下沉
- [ ] 多 provider 切换的集成测试（DeepSeek ↔ Anthropic 格式差异、缓存策略差异）

---

## P2 — 新能力建设

### ACP 增强 🟢

**当前状态**: `internal/acp/` 有 protocol/server/dispatch/service，CLI 已挂载 `acp` 子命令。

**缓存分析**: Agent 间调用 = 各自的独立 session，隔离性等同于 Coordinator 的双模型设计，天然兼容。

**待做**:
- [ ] ACP 完整 spec 文档（协议格式、消息类型、错误处理）
- [ ] Agent 间工具调用转发：调用方将结果作为 tool result 追加，被调用方在自己的 session 中执行
- [ ] 跨 session 协作模式的工作目录和权限边界定义
- [ ] 递归调用检测和深度限制

### Sandbox Phase 2 🟢

**当前状态**: macOS Seatbelt 已实现。

**缓存分析**: 无关。

**待做**:
- [ ] Linux sandbox 实现（bubblewrap 优先）
- [ ] 沙箱逃逸检测：denied 操作自动识别并升级到 permission gate
- [ ] `[sandbox] bash` 的 Linux 等效配置

### MCP 增强 🟡

**当前状态**: `internal/plugin/` 支持 stdio + streamable-http。

**缓存分析**: 工具懒加载 = tools schema 可能在 session 中途变化 → cache break。全量启动加载反而是缓存更优策略。懒加载应仅在工具数量极大（>50）时作为可选项。

**待做**:
- [ ] MCP OAuth 2.0 认证流程
- [ ] `.mcp.json` 作用域支持（项目级 / 用户级合并）
- [ ] 工具懒加载（`tools/list` 按需），默认关闭，加 `[mcp] lazy = true` 显式启用并附带缓存警告
- [ ] Provider 插件——MCP server 提供自定义 LLM backend（单独的 session，不影响主 session 缓存）

---

## P3 — 体验与生态

### Desktop / Dashboard 🟢

**当前状态**: Tauri 壳存在 (`desktop/src-tauri/`)，Web dashboard 有构建产物 (`dashboard/dist/`)。

**待做**:
- [ ] Desktop 与 Go backend 的集成路径确认（HTTP SSE 已就绪）
- [ ] Session Tree 可视化——树形图展示对话分支
- [ ] **缓存面板**（高优先）：token 用量趋势、cache hit/miss 占比、prefix break 事件时间线、累计成本节省
- [ ] 多 session 并行视图：同时查看不同分支的执行状态

### 配置持久化 🟢

**当前状态**: 权限 "always allow" 只在 session 内存中生效。

**缓存分析**: 无关。

**待做**:
- [ ] "always allow" 确认后写入 `reasonix.toml` 或独立 `permissions.toml`
- [ ] 手动配置 vs 学习规则的冲突检测
- [ ] 规则过期/撤销机制

### 文档 🟢

- [ ] ACP 协议文档
- [ ] Goal 系统使用文档
- [ ] Plugin 开发指南（MCP 插件编写 + 缓存最佳实践）
- [ ] **缓存策略文档**：解释 prepend-only、reasoning pruning、compaction economics、PrefixShape 诊断的完整链路
- [ ] SPEC.zh-CN.md 与代码现状同步

---

## 已完成 ✓

- [x] Agent 主循环（streaming + tool dispatch + parallel reads）
- [x] 类型化事件流（13 种 Event Kind，Sink 接口）
- [x] Transport-agnostic Controller
- [x] Context Compaction（经济学判断 + 边界对齐 + 归档）
- [x] Cache Diagnostics（PrefixShape + rewrite version + 原因归因）
- [x] Two-model Coordinator（planner/executor 独立 session，缓存隔离）
- [x] Sub-agent Task Tool（工具白名单 + silent run）
- [x] Plan Mode（cache 友好的只读开关：不改 system/tools，仅 execute 时 gate）
- [x] Reasoning Pruning（`stripStaleReasoning`：tool-call 保留、纯文本剥离）
- [x] MCP Client（stdio + streamable-http）
- [x] 权限系统（allow/ask/deny 规则引擎）
- [x] macOS Seatbelt Sandbox
- [x] Session 持久化（JSONL + 原子写入）
- [x] Chat TUI（bubbletea v2）
- [x] HTTP+SSE Server
- [x] i18n（中英文）
- [x] Myers Diff
- [x] 斜杠命令系统
