# Changelog

## v2 — Go 重写 (2026-05-30)

reasonix v2 是从零开始的 Go 语言重写，保留了 v1 核心理念（配置驱动、插件架构、多模型协作），同时用类型安全的编译型语言重建了整个技术栈。

---

### 架构升级

- **类型化事件流**：Agent 不再直接输出 ANSI 文本，而是通过 `event.Sink` 接口发出 14 种结构化事件（推理流、文本增量、工具调度、工具结果、用量统计、权限请求、压缩进度等），前端只需消费事件即可渲染，无需解析文本前缀。

- **Transport-Agnostic Controller**：`control.Controller` 是统一的会话控制层，所有前端（TUI、HTTP+SSE、ACP）都通过它驱动 Agent 生命周期。Controller 管理 runner、executor、session、plan mode、权限批准，对外暴露 `Send`/`Cancel`/`Approve`/`Compact`/`NewSession`/`Resume` 等命令。

- **桌面应用基础**：
  - Myers 行差分引擎 — 写文件/编辑工具可在不落盘的情况下预览变更（unified diff），用于权限卡片和变更文件面板。
  - 自省 API — 提供 provider、工具、MCP 服务器、slash 命令的只读 JSON 投影，供设置 UI 使用。
  - 编程式配置管理 — `SetDefaultModel`/`UpsertProvider`/`UpsertPlugin` 等可验证的配置变更方法 + 原子化 `SaveTo`。
  - `builtin.Workspace` — 将内置工具绑定到指定工作目录，支持多 Agent（不同桌面标签页处理不同项目）并行且路径隔离。

---

### Chat TUI 增强

- **实时交互式补全**：Tab 触发 slash 命令和 @ 文件/MCP 资源的自动补全菜单，支持模糊匹配和使用频率加权排序。
- **压缩进度条**：Unicode 块字符（▰/▱）动态进度条 + 百分比 + 旋转提示，展示 compact 的完整阶段（bounds → archive → summarize → rebuild → done）。
- **交互式会话拾取器**：`/resume` 命令弹出可保存会话列表，上下箭头选择，Enter 恢复，Esc 取消，自动重播历史对话。
- **一键剪贴板图片粘贴**：Ctrl+V 在聊天框中检测剪贴板含图片时自动保存到临时文件并注入 `@` 引用，支持 MCP vision 工具分析。
- **新 slash 命令**：
  - `/clear` — 清空上下文，保留配置
  - `/doctor` — 诊断报告（API key、网络连接、配置验证、缓存统计）
  - `/config` — 查看当前配置
  - `/init` — 分析当前项目（检测语言和框架）
  - `/commands` — 自定义命令管理（创建/删除/列表）
  - `/img` — 剪贴板图片快捷引用
  - `/btw` — 临时提问，不保存到对话历史
  - `/effort` — 切换推理深度（auto/high/fast）

---

### Agent 核心改进

- **缓存驱动架构**：
  - `PrefixShape` 显式追踪 prompt 前缀稳定性，`CacheDiagnostics` 自动记录每轮命/失比和变化原因。
  - `/cache-report` 命令展示历史命中率图、prefix break 时间线、成本节省估算。
  - `stripStaleReasoning` — 纯文本回复的 `reasoning_content` 在下一轮前剥离（仅 tool-call 轮次保留），利用 DeepSeek 50x-120x cache hit 价差。

- **语义化上下文压缩**：
  - LLM 摘要按结构化模板输出（Goal / Decisions / Files / Commands / Pending）。
  - `KeepPolicy` 配置 `[agent] keep = ["errors", "user_marked"]`，可以在压缩时保留错误输出和用户标记的消息。
  - 可配的压缩阈值（`compact_ratio`）和保留数（`recent_keep`）。

- **思考力度控制**：通过 `[agent] effort = "auto"|"high"|"fast"` 配置推理深度，运行时可通过 `/effort` 命令实时切换。

---

### ACP 协议集成

- **JSON-RPC 2.0 stdio 传输层**：兼容 ACP v1 协议，Go 侧实现完整的 NDJSON framing、会话注册表、权限回合制、turn 取消。
- **`reasonix acp` 子命令**：通过 transport-agnostic Controller 驱动 Agent，每个会话拥有独立 MCP 工具集和 cwd。
- **会话持久化与恢复**：`session/new` 创建 transcripts（JSONL），`session/prompt` 每轮快照，`session/load` 跨重启恢复。
- **端到端测试**：hermetic（脚本化 provider + 假工具）和 live（真实模型调用，`DEEPSEEK_API_KEY` 门控）两套 e2e 测试。

---

### HTTP+SSE 服务

- **`reasonix serve` 命令**：一个 HTTP 服务器驱动一个 session（SSE 广播事件），支持多浏览器标签页共享。
- **REST API**：`GET /events`（SSE）、`GET /history`、`GET /context`、`POST /submit`、`POST /cancel`、`POST /approve`、`POST /compact`、`POST /new`。
- **嵌入式浏览器客户端**：最小的 index.html（EventSource + fetch）开箱即用。

---

### Session Tree（会话树）

- **树形会话分支**：`/branch <名称>` 从当前会话分叉，`/switch <id>` 切换到已有分支，`/tree` 查看分支树。每分支独立消息历史，共享祖先 prefix 天然复用缓存。
- **树持久化**：每个 session 自动保存 `.tree.jsonl` 文件，与线性 session 文件共存。

---

### 权限 & 安全

- **Bash 只读识别**：自动识别 `ls`/`cat`/`grep`/`git log` 等只读命令，提升为 `readOnly=true` 跳过交互式权限门控。
- **macOS Seatbelt Sandbox**：进程级沙箱限制文件系统和网络访问。

---

### i18n 国际化

- **完整中英文双语支持**：所有 CLI 界面文本（欢迎页、chat TUI 状态栏、slash 命令输出、init 向导、help 文本、错误提示）均已翻译。
- **反射式漂移检测**：`TestCatalogsComplete` 测试通过反射确保所有语言的翻译字段完整，缺译在 CI 阶段即被拦截。
- **运行时切换**：`/lang en|zh` 命令即时切换，自动持久化到 `reasonix.toml`。
- **自动检测**：优先级 `REASONIX_LANG > LC_ALL > LC_MESSAGES > LANG > "en"`。

---

### Provider 层

- **DeepSeek 兼容修复**：空 content 的纯 tool_calls 轮次现在强制序列化 `"content":""` 字段，修复 DeepSeek 严格反序列化器拒绝请求的问题。
- **Thinking Effort 透传**：`Effort` 字段通过 OpenAI provider 映射为 DeepSeek 的 `thinking` 线控（enabled/disabled/max tokens）。

---

### 技术细节

- **scrollback 稳定性**：bubbletea v2 的 inline renderer 按屏幕高度分块提交 scrollback，过宽行自动硬断行，确保输入框 border 不漂移。
- **类型安全**：从 v1 的 TypeScript/JavaScript 运行时动态检查，完全迁移到 Go 编译时类型系统。
