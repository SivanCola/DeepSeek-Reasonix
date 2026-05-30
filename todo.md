# Reasonix Todo

> 基于 Pi Agent 设计理念对照当前代码状态的改进清单。
> 优先级：P0 > P1 > P2 > P3

---

## P0 — 核心架构完善

### Session Tree（树形会话）

**Pi Agent 启发**: 会话以树组织，天然支持分支、回退、多路径探索。

**当前状态**: `internal/agent/session.go` 是线性 JSONL 追加。`save.go` 只支持顺序加载。

**待做**:
- [ ] 设计 SessionNode 结构：`id`, `parentID`, `children[]`, `messages[]`, `metadata`
- [ ] `Session.Branch()` — 从任意节点分叉
- [ ] `Session.Merge()` — 合并分支的策略（取最优 / 用户选择）
- [ ] `Save/Load` 支持树形结构的序列化
- [ ] TUI 中展示 session 树，支持在分支间导航

### Goal 系统集成

**当前状态**: `internal/agent/goal/` 已有 `goal.go` 和 `auto_test.go`，但与 Agent 主循环的集成不明确。

**待做**:
- [ ] Goal 如何注入 Agent loop——是 plan mode 的前置步骤还是独立阶段？
- [ ] Goal 的自动拆解（高层面目标 → 可执行子任务）
- [ ] Goal 完成度追踪与重规划触发条件
- [ ] 与 Coordinator（planner/executor 双模型）的关系梳理——避免重复机制

---

## P1 — 现有能力强化

### Context Compression 语义化

**Pi Agent 启发**: 压缩不是截断，是保留语义、丢掉冗余。

**当前状态**: `internal/agent/compact.go` 使用 summarize（调 LLM 蒸馏中间区域），已有 `foldEconomics` 经济性判断。

**待做**:
- [ ] 结构化摘要格式：按 topic/task 分段而非一段自由文本
- [ ] 压缩前后关键信息保留率评估（sanity check: 压缩后的 session 能否回答之前的关键问题？）
- [ ] 可配置的保留策略：保留 tool results 的 error 部分、保留用户明确标记为重要的消息
- [ ] `cache_shape.go` 的 PrefixShape 在压缩后自动失效，考虑是否要做 incremental 压缩（保持前缀不变）

### Agent Loop 可测试性

**Pi Agent 启发**: Demo 级别的增量可测——mock LLM、验证 loop 决策而非真实调用。

**当前状态**: `builtin_test.go` 有工具测试，但 Agent 主循环缺少 mock provider 的测试。

**待做**:
- [ ] `MockProvider` — 返回预定义 messages，用于测试 loop 决策
- [ ] Agent loop 单元测试：给定 message 序列，验证 tool dispatch 逻辑、compact 触发、maxSteps 终止
- [ ] 事件流验证：Run 后检查 emit 的事件种类和顺序
- [ ] 并行 read 工具调度的正确性测试（顺序不能乱）

### Provider 层更清晰的抽象

**Pi Agent 启发**: pi-ai / pi-agent-core / pi-coding-agent 三层分离。

**当前状态**: `internal/provider/provider.go` 接口已存在，OpenAI 实现也独立。但 `agent.go` 中仍有 LLM 特定的行为（如 `reasoning_content` 处理）。

**待做**:
- [ ] 排查 `agent.go` 中与具体 provider 耦合的逻辑，下沉到 `provider/` 层
- [ ] `reasoning.go` 的 `stripStaleReasoning()` 是否应作为 provider 的可选能力而非 agent 的全局假设
- [ ] 多 provider 切换的集成测试（DeepSeek ↔ Anthropic 格式差异）

---

## P2 — 新能力建设

### ACP（Agent Communication Protocol）

**当前状态**: `internal/acp/` 有 protocol/server/dispatch/service，CLI 已挂载 `acp` 子命令。端到端测试存在但可能未完整。

**待做**:
- [ ] ACP 的完整 spec 文档（协议格式、消息类型、错误处理）
- [ ] Agent 间工具调用转发（agent A 调用 agent B 暴露的工具）
- [ ] 跨 session 的 agent 协作模式
- [ ] ACP 的权限边界——agent 能做什么、不能做什么

### Sandbox Phase 2

**当前状态**: macOS Seatbelt 已实现。剩余 Linux bubblewrap/landlock 和 escape detection。

**待做**:
- [ ] Linux sandbox 实现（bubblewrap 优先）
- [ ] 沙箱逃逸检测：denied 操作自动识别并升级到 permission gate
- [ ] 沙箱内文件系统隔离范围可配置

### MCP 增强

**当前状态**: `internal/plugin/` 支持 stdio 和 streamable-http。OAuth 未实现。

**待做**:
- [ ] MCP OAuth 2.0 认证流程
- [ ] `.mcp.json` 作用域支持
- [ ] 工具懒加载（按需 `tools/list`，避免启动时全量加载）
- [ ] Provider 插件——允许 MCP server 提供自定义 LLM backend

---

## P3 — 体验与生态

### Desktop / Dashboard

**当前状态**: Tauri 壳存在 (`desktop/src-tauri/`)，Web dashboard 有构建产物 (`dashboard/dist/`)。

**待做**:
- [ ] 确认 desktop/dashboard 与 Go backend 的集成路径（HTTP SSE 已有，需确认前端是否完整对接）
- [ ] Desktop 中 session tree 的可视化——树形图展示对话分支
- [ ] Dashboard 中的 token 用量和 cache 命中率面板

### 配置持久化

**当前状态**: 权限 "always allow" 只在 session 内存中生效，不写回文件。

**待做**:
- [ ] 用户确认 "always allow" 后写入 `reasonix.toml` 或独立 `permissions.toml`
- [ ] 冲突检测——手动配置 vs 学习到的规则
- [ ] 过期/撤销机制

### 文档

- [ ] ACP 协议文档
- [ ] Goal 系统使用文档
- [ ] Plugin 开发指南（如何写一个 MCP 插件给 Reasonix 用）
- [ ] SPEC.zh-CN.md 与代码现状同步更新

---

## 已完成 ✓

- [x] Agent 主循环（streaming + tool dispatch + parallel reads）
- [x] 类型化事件流（13 种 Event Kind，Sink 接口）
- [x] Transport-agnostic Controller
- [x] Context Compaction（经济学判断 + 边界对齐 + 归档）
- [x] Cache Diagnostics（PrefixShape + rewrite version）
- [x] Two-model Coordinator（planner/executor 独立 session）
- [x] Sub-agent Task Tool（工具白名单 + silent run）
- [x] Plan Mode（cache 友好的只读开关）
- [x] MCP Client（stdio + streamable-http）
- [x] 权限系统（allow/ask/deny 规则引擎）
- [x] macOS Seatbelt Sandbox
- [x] Session 持久化（JSONL + 原子写入）
- [x] Chat TUI（bubbletea v2）
- [x] HTTP+SSE Server
- [x] i18n（中英文）
- [x] Myers Diff
- [x] 斜杠命令系统
