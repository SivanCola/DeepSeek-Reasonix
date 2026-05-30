# Reasonix 工程规范

> Reasonix 是一个编码智能体：一个薄层调度器驱动多个模型，**所有能力由配置和插件提供**。
> 本文档即为契约——代码遵循它。先修改契约，再修改代码。

## 1. 设计原则

1. **配置和插件驱动的核心。** 核心只认识接口。具体的模型和工具按名称从注册表中解析，
   在配置中声明，或由插件注入。没有硬编码的 `switch model`。
2. **单一静态二进制。** `CGO_ENABLED=0`；一条命令交叉编译；CLI 开箱即用。
3. **精简依赖。** 默认使用标准库。引入第三方依赖必须是纯 Go、轻量级，且不能损害
   单二进制 / 跨平台 / 分发的目标。TOML 解析是唯一已接受的依赖。
4. **两层扩展机制。** 编译期内置（通过 `init()` 自注册），和运行时外部插件
   （stdio JSON-RPC 子进程，兼容 MCP）。
5. **接口优先 & 基于注册表。** `Provider` 和 `Tool` 都是接口。
6. **演进，不过度工程化。**

语言：**英语是所有代码的主要语言**——注释、用户可见字符串、工具描述、系统提示
以及本规范。README 为双语（`README.md` 英文 + `README.zh-CN.md`）。

## 2. 目录布局

```
reasonix/
├── go.mod / go.sum          # 模块 reasonix；依赖 BurntSushi/toml
├── Makefile                 # build / cross / vet / fmt / test
├── README.md / README.zh-CN.md
├── reasonix.example.toml         # 示例配置
├── docs/SPEC.md             # 本文件
├── cmd/reasonix/main.go          # 入口；空导入内置 provider/tool
├── cmd/reasonix-plugin-example/  # 参考 MCP stdio 插件（可运行示例）
└── internal/
    ├── cli/                 # 子命令路由、标志、组装、退出码
    ├── config/              # TOML 加载（标志 > 项目 > 用户 > 默认值）
    ├── provider/            # Provider 接口 + 类型 + kind→factory 注册表
    │   └── openai/          # OpenAI 兼容实现；init() 注册 "openai"
    ├── tool/                # Tool 接口 + Registry
    │   └── builtin/         # read_file/write_file/edit_file/bash/ls/glob/grep
    ├── permission/          # 逐次调用策略：allow/ask/deny 规则 → Decision
    ├── command/             # 从 .reasonix/commands/*.md 加载的自定义斜杠命令
    ├── plugin/              # stdio JSON-RPC (MCP) 客户端；适配远程工具
    └── agent/               # Session + harness loop
```

依赖方向（无环）：`cli → {agent, plugin, config} → {tool, provider}`。
内置子包（`provider/openai`、`tool/builtin`）导入其父包以自注册；父包绝不导入子包。

## 3. 核心抽象

### 3.1 Provider + 注册表 (`internal/provider`)

```go
type Provider interface {
    Name() string
    Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}

// Factory 从已解析的配置实例构建 Provider。
type Factory func(cfg Config) (Provider, error)

// Register 在 kind（如 "openai"）下添加工厂。从 init() 调用。
func Register(kind string, f Factory)

// New 实例化给定 kind 的 provider。
func New(kind string, cfg Config) (Provider, error)

type Config struct {
    Name    string         // 实例名称，如 "deepseek"
    BaseURL string
    Model   string
    APIKey  string
    Extra   map[string]any // kind 特定的选项
}
```

- `openai` 类型是 OpenAI 兼容的 `/chat/completions` 实现。
- **DeepSeek 和 MiMo 不是代码——它们是 `kind = "openai"` 的配置实例**，
  仅在 `base_url` / `model` / `api_key_env` 上有区别。添加另一个 OpenAI 兼容
  的模型只需修改配置，无需修改代码。
- 流式的 tool-call delta 在 provider 内部按索引累积；只发出完整的 `ToolCall`。

### 3.2 Tool + 注册表 (`internal/tool`)

```go
type Tool interface {
    Name() string
    Description() string
    Schema() json.RawMessage // 参数的 JSON Schema
    Execute(ctx context.Context, args json.RawMessage) (string, error)
}
```

- 内置工具通过 `init()` 自注册到进程全局的内置工具集
  （`tool.RegisterBuiltin(t)`）；`tool.Builtins()` 列出它们。
- 每次运行组装一个运行时 `*Registry`：启用的内置工具（按配置过滤）
  **加上**插件提供的工具。智能体只看到 `*Registry`。
- `Execute` 自行解析原始 JSON 参数。错误被返回，而非致命——智能体将其
  反馈给模型以便自我修正。

### 3.3 插件 (`internal/plugin`) — MCP 客户端

外部插件是在配置中声明的 MCP 服务器。线协议在所有情况下都是
**JSON-RPC 2.0**；仅传输方式不同。`transport` 接口
（`call` / `notify` / `close`）对此进行抽象，因此 MCP 层面的逻辑
（handshake、`tools/list`、`tools/call`……）只需编写一次。

- **传输方式**（配置 `type`）：
  - `stdio`（默认）——本地子进程；通过子进程的 stdin/stdout 每行一条
    JSON 消息（MCP stdio 约定）。使用 `command` / `args` / `env` 声明；
    在 ctx 取消 / 关闭时终止。
  - `http`（即 `streamable-http`）——位于 `url` 的远程服务器。每个请求
    为 HTTP POST；服务器回复 `application/json`（一次响应）或
    `text/event-stream`（携带响应及任意服务器通知的 SSE 流）。
    `Mcp-Session-Id` 响应头一经获取，在后续请求中回传。静态 `headers`
    （如 bearer token）在每个请求中发送。OAuth 暂不在范围内（见 §9）。
  - `sse` ——旧的 2024-11-05 HTTP+SSE 传输方式；可识别但延期处理
    （上游已弃用——请使用 `http`）。配置它会返回明确错误。
- `${VAR}` / `${VAR:-default}` 在 `command`、`args`、`env`、`url` 和
  `headers` 中展开，因此密钥来自环境变量而非配置文件。
- 生命周期：`initialize` → `notifications/initialized` → `tools/list`；
  通过 `tools/call {name, arguments}` 调用。
- 每个远程工具适配为 `Tool` 接口并注入到运行时注册表中，命名空间为
  `mcp__<server>__<tool>`（空格规范化为 `_`），以匹配 Claude Code
  并避免命名冲突。
- 工具的 MCP `annotations.readOnlyHint` 映射到 `Tool.ReadOnly()`。默认
  为 false（远程工具不透明——我们无法看到其副作用），因此插件通过
  在 `tools/list` 中声明 `readOnlyHint: true`，将工具选入并行批处理
  调度和权限层的 reader-default。
- `prompts/list` + `prompts/get` 展示为 `/mcp__<server>__<prompt>` 斜杠
  命令；`resources/list` + `resources/read` 在聊天中引用为
  `@<server>:<uri>`。`/mcp` 显示已连接服务器及其数量。
- `cmd/reasonix-plugin-example` 是一个可运行的参考 stdio 服务器
  （`echo`、`wordcount`），由构建真实二进制的端到端测试驱动。

### 3.4 Agent (`internal/agent`)

- `Session` 持有 `[]Message`。
- `Run(ctx, input)` 循环：构建 `Request`（带有工具 schema）→ `provider.Stream`
  → 实时打印文本增量，收集完整的 tool call → 如果没有，结束；否则
  执行每个工具（内置或插件）并将结果追加 → 重复，受 `maxSteps` 限制。
  `ctx` 贯穿始终（Ctrl-C 中止进行中的请求）。
- `Runner` 是任何具有 `Run(ctx, input) error` 的对象；`Agent` 和
  `Coordinator` 都满足它，因此 CLI 对单模型与双模型模式无关。

### 3.5 双模型协作 (`Coordinator`)

当 `agent.planner_model` 指定了与执行器不同的 provider 时，
`Coordinator` 在**独立会话**中运行两个模型，以保持各自提示前缀缓存稳定：

- **规划器**（低频）在其自身会话中运行，无工具，产出简洁计划。
- 该计划作为结构化文本移交给**执行器**——在其自身会话中的完整
  工具使用 `Agent`——由其执行。
- 两个会话从不混合，因此两个模型的提示前缀都不会被对方的轮次干扰；
  两者都以仅追加方式增长并保持缓存友好。这调和了"缓存优先"与
  "双模型协作"：在一个共享对话中切换模型会破坏前缀并导致缓存命中率
  急剧下降，所以我们不这样做。

### 3.6 上下文管理（压缩）

长任务最终会填满模型的上下文窗口。Reasonix 采用尊重缓存优先设计的
**低频压缩**来管理：

- 每个 provider 声明其 `context_window`（token）。当某一轮报告的
  `prompt_tokens` 达到该窗口的 `compactRatio`（默认 `0.8`）时，执行器
  在下一轮之前**一次性**压缩。
- 压缩使用执行器自身的 provider，无工具，将会话中较早的中间部分总结为
  一份简报，并就地替换：会话变为 `system + summary + recentKeep`
  （默认 `8`）条逐字消息。边界向后对齐到工具结果，以便最近尾部不会以
  孤儿工具消息开头（其 `tool_calls` 已被摘要掉）。
- 被丢弃的原始消息归档到
  `~/.config/reasonix/archive/<timestamp>.jsonl`
  （每行一条消息），以便完整历史可追溯。

这是提示前缀发生变化的**唯一**节点——一个刻意的、罕见的
"缓存重置点"。在两次压缩之间，会话以仅追加方式增长并保持缓存友好，
因此缓存命中率（关键的观测信号）保持高水平。`context_window = 0`
禁用某实例的压缩。

### 3.7 权限 (`internal/permission`) — 逐次调用门控

编码智能体自主运行 shell 命令和编辑文件。权限层**针对每次工具调用**
决定是允许、拒绝还是先询问用户。它独立于模型和 CLI——智能体在执行时
查询 `Gate` 接口；该门由静态 `Policy` 加上可选的交互式 `Approver` 构建。

```go
type Decision int            // permission 包
const (Allow Decision = iota; Ask; Deny)

// Policy 根据工具调用评估静态规则。纯函数，无 I/O。
type Policy struct { Mode Decision; Allow, Ask, Deny []Rule }
func (p Policy) Decide(toolName string, readOnly bool, args json.RawMessage) Decision
```

- **规则语法。** 一条规则是 `ToolName`（匹配对该工具的任何调用）或
  `ToolName(glob)`（当调用的*主体*通过 `path.Match` 匹配 glob 时匹配）。
  主体从调用的 JSON 参数中通过一组已知键名（`command`（bash）、
  `path` / `file_path`（文件工具）、`pattern`（grep/glob））泛化提取，
  因此工具无需修改。对于参数中无法提取主体的规则，仅以其裸 `ToolName`
  形式匹配。
- **优先级。** `deny` > `ask` > `allow` > 回退。对于只读工具，回退为
  `Allow`；对于写工具，回退为 `Mode`（默认 `Ask`）。`deny` 始终优先，
  因此宽泛的 `allow = ["bash"]` 可以被 `deny = ["bash(rm -rf*)"]` 切割；
  反之 `ask` 覆盖宽泛的 `allow`，强制对风险子集进行提示。
- **解析 `Ask`。** 交互式前端（聊天 TUI）通过 `Approver` 提示用户
  ——允许一次 / 始终允许 / 拒绝。非交互式运行
  （`reasonix run`、子智能体、任何无 TTY / 无 approver 的情况）
  无法提示，因此将 `Ask` 解析为**允许**——保持自主行为。`Deny` 在
  *所有*模式下都是硬阻止：工具绝不执行，模型收到"blocked"结果后
  可自行适应（与计划模式拒绝的形状相同）。
- **与计划模式的关系。** 计划模式（§3.4）是正交的、更粗粒度的门控，
  无论策略如何，拒绝所有写操作；它先被检查。权限层是其下细粒度的、
  始终启用的门控。

开箱即用（`mode = "ask"`，无规则）`reasonix run` 行为完全如旧
（写操作在无 TTY 时解析 `Ask`→allow），而 `reasonix chat` 现在在每次
写/bash 调用前提示。`deny` 规则在两种模式下都加固安全。

### 3.8 斜杠命令 (`internal/command`)

聊天 TUI 接受 `/command` 输入。三种类型共享一个分发机制：

- **内置操作**（`/compact`、`/new`、`/mcp`、`/help`）本地操作会话状态，
  绝不到达模型。
- **自定义命令**是 `.reasonix/commands/`（项目）和
  `~/.config/reasonix/commands/`（用户）下的 Markdown 文件；
  项目目录在名称冲突时覆盖用户目录。文件 `review.md` 变为 `/review`；
  子目录进行命名空间化（`git/commit.md` → `/git:commit`）。调用后渲染
  其正文并将结果作为下一个用户轮次发送。
- **MCP prompts**（§3.3）展示为 `/mcp__<server>__<prompt>`。

```markdown
---
description: Review the staged diff
argument-hint: [focus-area]
---
Review the staged diff. Focus on $ARGUMENTS, list bugs with file:line.
```

- Frontmatter 是可选的 `---` 分隔块，包含简单的 `key: value` 行；
  `description` 和 `argument-hint` 被识别（无 YAML 依赖——Reasonix
  保持精简）。其余为正文模板。
- 正文中的替换：`$ARGUMENTS`（所有参数，空格连接）、`$1`…`$N`
  （位置参数，缺失时为空）、`$$`（字面量 `$`）。参数是命令后的
  空格分隔 token。
- 加载是纯函数（`command.Load(dirs...)`）并已测试；格式错误的文件
  被跳过，不致命。自定义命令和 MCP-prompt 命令都解析为文本，并复用与
  输入消息相同的"启动轮次"路径。

### 3.9 聊天引用 (`@`)

聊天消息可以嵌入 `@` 引用；在发送轮次之前，每个引用被解析并作为标记块
前置到消息中，供模型阅读。

- `@<server>:<uri>` 其中 `<server>` 是已连接的 MCP 服务器 → MCP 资源
  （`resources/read`），包裹为 `<resource ref="…">…</resource>`。
- `@<path>` 否则 → **本地文件或目录**，但仅当路径实际存在于磁盘上时。
  此存在性检查是消歧器：普通的 `@mention` 或电子邮件地址解析不到文件，
  保持字面文本。文件包裹为 `<file path="…">…</file>`
  （有大小限制，二进制文件注明而不转储）；目录变为一级列表。
- 解析是异步的（脱离 TUI 事件循环）；获取失败显示为通知但不阻塞轮次。
  读取是用户发起的且只读——它们**不**通过权限门控（§3.7）。
- 输入 `/` 或 `@` 会在输入框上方打开自动补全菜单。`@` 菜单
  **一次一级目录**进行导航（`os.ReadDir`，绝不递归遍历——对巨型目录
  有界）：目录条目进入下一级，文件完成补全，MCP 资源出现在顶级条目
  旁边。底部区域菜单仅在这些离散操作时改变高度，而不是每个流式 token
  都改变，因此回滚区域保持干净（§ 渲染）。

## 4. 数据类型 (`internal/provider`)

```go
type Role string
const (RoleSystem Role = "system"; RoleUser Role = "user"
       RoleAssistant Role = "assistant"; RoleTool Role = "tool")

type Message struct {
    Role       Role       `json:"role"`
    Content    string     `json:"content,omitempty"`
    ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
    ToolCallID string     `json:"tool_call_id,omitempty"`
    Name       string     `json:"name,omitempty"`
}

type ToolCall   struct { ID, Name, Arguments string }              // Arguments: raw JSON
type ToolSchema struct { Name, Description string; Parameters json.RawMessage }
type Request    struct { Messages []Message; Tools []ToolSchema; Temperature float64; MaxTokens int }

type ChunkType int
const (ChunkText ChunkType = iota; ChunkToolCall; ChunkDone; ChunkError)

type Chunk struct {
    Type     ChunkType
    Text     string    // ChunkText
    ToolCall *ToolCall // ChunkToolCall
    Err      error     // ChunkError
}
```

## 5. 配置 (TOML)

解析顺序：**标志 > 项目 `./reasonix.toml` > 用户
`~/.config/reasonix/config.toml` > 内置默认值**。密钥通过
`api_key_env` 来自环境变量，绝不存储在配置文件中。工作目录中的
`.env` 文件（如果存在）会被加载。

```toml
default_model = "deepseek-flash"   # 执行器
# language    = "zh"                # ui 语言标签；空 = 从 $LANG / $REASONIX_LANG 自动检测

[agent]
system_prompt = "You are Reasonix, a coding agent..."  # 或 system_prompt_file = "..."
max_steps     = 25
temperature   = 0.0
# planner_model = "mimo"   # 可选：双模型协作（低频规划器）

[[providers]]
name           = "deepseek-flash"
kind           = "openai"
base_url       = "https://api.deepseek.com"
model          = "deepseek-v4-flash"
api_key_env    = "DEEPSEEK_API_KEY"
context_window = 1000000   # token；调度器在此限制附近压缩较早历史（0 禁用）

[[providers]]
name        = "deepseek-pro"
kind        = "openai"
base_url    = "https://api.deepseek.com"
model       = "deepseek-v4-pro"
api_key_env = "DEEPSEEK_API_KEY"

[[providers]]
name        = "mimo-pro"
kind        = "openai"
base_url    = "https://api.xiaomimimo.com/v1"
model       = "mimo-v2.5-pro"
api_key_env = "MIMO_API_KEY"

[[providers]]
name        = "mimo-flash"
kind        = "openai"
base_url    = "https://api.xiaomimimo.com/v1"
model       = "mimo-v2-flash"
api_key_env = "MIMO_API_KEY"

[tools]
enabled = []   # 省略/空 = 全部内置工具

[permissions]
mode  = "ask"                              # 无规则匹配时写操作回退：ask|allow|deny
deny  = ["bash(rm -rf*)", "bash(git push*)"]   # 所有模式下硬阻止
allow = ["bash(go test*)", "bash(git status*)"]  # 从不提示
ask   = []                                 # 即使其他方式已允许，也强制提示

[sandbox]
# workspace_root = ""          # 文件写入器限定于此；空 = cwd（写入保留在项目内）
# allow_write    = ["/tmp"]    # write_file/edit_file/multi_edit 可修改的额外目录

[[plugins]]
name    = "example"            # type 默认为 "stdio"
command = "reasonix-plugin-example"
args    = []
# env   = { FOO = "bar" }

# [[plugins]]                   # 通过 Streamable HTTP 的远程 MCP 服务器
# name    = "stripe"
# type    = "http"             # "stdio"（默认）| "http" | "sse"
# url     = "https://mcp.stripe.com"
# headers = { Authorization = "Bearer ${STRIPE_KEY}" }   # ${VAR} / ${VAR:-default} 被展开
```

`reasonix init` 写入此默认配置，使 CLI 开箱即用。

MCP 服务器也可以在项目根目录的 `.mcp.json` 中声明，使用 Claude Code
精确的 `mcpServers` schema（`command`/`args`/`env`、`type`/`url`/`headers`、
`${VAR}` 展开）。它在 TOML 文件之后读取并合并到 `[[plugins]]` 中；
在名称冲突时 `reasonix.toml` 优先（它是更明确、Reasonix 专用的来源）。
这使得已在 Claude 中配置的服务器可以在 Reasonix 中直接使用。

```json
{ "mcpServers": {
  "stripe": { "type": "http", "url": "https://mcp.stripe.com",
              "headers": { "Authorization": "Bearer ${STRIPE_KEY}" } }
} }
```

`[sandbox]` 是权限之下的*执行*层（权限是*策略*）。Phase 0 将文件写入
内置工具（`write_file`、`edit_file`、`multi_edit`）限定到 `workspace_root`
（默认 cwd）加 `allow_write`：目标路径——解析为绝对、无符号链接的路径，
防止符号链接目录或 `..` 逃逸——若不在任何根内，则写入被拒绝，错误反馈
给模型。限制默认开启（root = cwd），因此编辑保留在项目内；读取不受限。
`bash` 在 macOS 上默认也被监禁（`[sandbox] bash = "enforce"`，Seatbelt）：
每个命令在 sandbox-exec 下运行，仅允许写入相同根目录（+ 临时目录和
工具链缓存），仅当 `network = true` 时允许联网。不受支持的平台回退到
无限制运行。逃逸提示和 Linux 支持是 Phase 1 的剩余工作（§9）。

## 6. 错误处理

- 库代码用 `fmt.Errorf("...: %w", err)` 包装并返回；绝不打印或调用
  `os.Exit`。
- 仅 `cli` / `main` 决定退出码和用户可见消息。
- 工具执行错误反馈给模型，不致命。
- 网络层应对 429 / 5xx 应用有限指数退避
  （接口已预留；实现可后续跟进）。

## 7. 代码风格

- `gofmt` + `go vet` 必须干净；包名小写；导出标识符有文档；
  注释解释*为什么*，而非*是什么*。
- 不过早泛化。偏好清晰直接。

## 8. 分发

- 构建：`CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=$(VERSION)" -o reasonix ./cmd/reasonix`
- 交叉编译矩阵：`darwin|linux|windows` × `amd64|arm64`。
- 版本通过 ldflags 注入（`git describe --tags --always`）。
- 安装：预编译二进制 / `go install` / 未来的 `brew tap`。

## 9. 路线图（当前不在范围内）

- Sandbox Phase 1：为 `bash` 提供操作系统级监禁，使命令——
  而不仅仅是文件写入内置工具（Phase 0）——被限定到工作区。**macOS
  （通过 `sandbox-exec` 的 Seatbelt）已发布，默认开启**（见 §5）。
  剩余工作：(a) 逃逸提示——检测 sandbox 拒绝的失败并提议通过权限门控
  无限制地重新运行命令（在 `reasonix run` 中，命令直接失败，模型自行适应），
  这完善了"框内允许，在边界提示"的模型；(b) Linux（bubblewrap / landlock）。
  调用操作系统工具，因此二进制保持零依赖；Windows 不在范围内。有了这些，
  "始终允许"的规则持久化将变为可选而非关键依赖。
- MCP 长尾（有意延期——暂无消费者 / 无基础）：远程服务器的 OAuth 2.0 +
  `headersHelper` 认证；剩余的 `.mcp.json` 作用域
  （local / user——project 作用域已交付，见 §5）；工具搜索延迟加载；
  `list_changed` 实时更新；channels / elicitation / roots；提供
  *provider*（而不仅仅是工具）的插件。
- Anthropic 原生 provider `kind`（原生 prompt-cache 控制），证明注册表
  可泛化到一种线格式之外。
- "始终允许"持久化，将学到的规则写回项目配置；`reasonix run` 的
  按会话权限覆盖标志。
