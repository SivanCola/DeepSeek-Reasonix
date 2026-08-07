# desktop-lite 交接快照

> 快照，不是流水账。改动后更新对应段、删过时内容。

## 基础信息

**是什么**：Reasonix 的精简桌面版——在高缓存率基础上最大化释放模型性能，最大化支持
MCP/skill 调度，把用户配置成本压到最低，**不再强制多项目多会话**。

**技术栈**：Go 1.25（内核复用 `reasonix/internal/*`）+ Wails（外壳，尚未搭）+
React/Vite（前端，尚未搭）。

**模块关系**：独立嵌套模块 `reasonix/desktop-lite`，`replace reasonix => ../`
复用同一套内核，与现有 `desktop/` 完全并存、互不影响。

**为什么是独立模块**：`desktop/` 前端 74k 行 TSX / 90 组件，`SettingsPanel.tsx`
单文件 7448 行，`app.go` + `tabs.go` 21963 行。精简版重写前端比裁剪便宜一个数量级。

**怎么跑**

```bash
# 内核层（免 cgo，无头可测）——日常开发跑这个就够
cd desktop-lite && go test ./...
cd desktop-lite && CGO_ENABLED=1 go test -race ./...

# 前端
cd desktop-lite/frontend && pnpm install && pnpm build

# 外壳（必须带 Wails 的 desktop,production；webkit2gtk-4.1 环境再加 webkit2_41）
cd desktop-lite && go build -tags "desktop,production,webkit2_41" -o reasonix-lite .

# 无显示器的服务器上验证能否启动
Xvfb :99 -screen 0 1280x800x24 &
DISPLAY=:99 ./reasonix-lite
DISPLAY=:99 xwininfo -root -tree | grep Reasonix   # 确认窗口存在
```

⚠️ 顺序有依赖：`main.go` 用 `//go:embed all:frontend/dist` 嵌入前端，**改了前端必须
先 `pnpm build` 再编译 Go**，否则嵌进去的是旧产物。

## 进度

**已完成**

- `internal/tool` deferred 分层（在**根模块**，非本模块）
  - `deferred.go`：`AddDeferred` / `Defer` / `Activate` / `DeferredRoster` /
    `PinPrefix` / `RenderDeferredRoster` / `Availability`
  - `search.go`：`tool_search` 工具，支持 `select:名字`、关键字、`+必需词`
  - `tool.go`：Registry 分层字段、`Schemas()` 追加式尾部、抽出 `addLocked`
- `internal/control`：`Controller.ToolRegistry()` 导出访问器
- `desktop-lite/internal/session`：单会话运行时 `Host` + deferred 接线
  - `session.go`：`Host` 生命周期，generation 守卫防 stale completion
    （已验证拆掉守卫测试即红）
  - `tools.go`：`wireDeferredTools` 按策略降级 + 装 `tool_search`；roster 走
    首轮消息注入，新连上的 server 下一轮补充公告，不重复已公告的
  - `stream.go`：`TranslateEvent` 把内核事件翻译成 UI 帧，含会话累计缓存命中率；
    operator 通知按内核契约不进用户对话流
- **Wails 外壳**：`main.go` / `app.go` / `wails.json`，只做窗口、绑定和事件转发
- **最小前端**：React + Vite，7 个依赖（现有 desktop 是 30+）。构建产物
  193KB JS / 1.77KB CSS
- **49 个测试**（tool 24 + session 25），含 `-race`
- 服务器构建工具链：gcc / pkg-config / gtk+-3.0 / webkit2gtk-4.1 / xvfb 已装
- **已实测启动**：Xvfb 下窗口正常打开（980x720），前端渲染，`ready` 帧送达并解锁
  输入框——Go→JS 通路与内核装配均已验证

**进行中**：无

**已实盘验证**（`live_test.go`，默认跳过，需 `REASONIX_LIVE=1` + 一次性 `REASONIX_HOME`）

- 真实 provider 跑通一轮对话，答案正确，usage/缓存命中率帧正常到达
- 接真实 MCP server（`@modelcontextprotocol/server-everything`，14 个工具）实测
  **冷 schema 缓存 + 生产接线顺序**下：

  | 指标 | 值 |
  |---|---|
  | 注册工具（无此机制时发送） | 62 个 / 56,514 schema 字节 |
  | 实际导出 | 48 个 / 50,092 字节 |
  | 每请求节省 | **6,422 字节（11.4%）≈ 1,605 tokens** |

  ⚠️ 11.4% 是**这个场景**的数字：参考 server 的 14 个工具都很小（均 ~270 字节），而
  47 个内置工具占了约 49KB 且不该 defer。收益随 MCP 数量和 schema 肥瘦增长，真实
  server（figma / playwright 之类）远不止于此。**尚未测过多 server、大 schema 的场景。**

**待办**（按建议顺序）

1. **⌘K 命令面板** — 用它取代设置面板，是 7448 行 → ~200 行的关键手法。
3. **配置推导** — 继续读同一份 `config.toml`（兼容），UI 只暴露 provider + 凭据，
   其余全部计算默认值。现有配置有 246 个 TOML 字段，目标暴露面 5–8 项。
4. **会话持久化 / 恢复** — 目前关掉就没了，内核有 checkpoint 能力可复用。

**降级时机（重要）**：`Defer` 只能在 boot 之后、首轮之前调用。此时还没有任何 provider
请求，没有已缓存的前缀可丢；晚一轮调用就会白扔那一轮的缓存。`wireDeferredTools` 在
`Open` 里、会话对外可见之前执行，正是为此。

## 三条必须守住的缓存不变量

改任何涉及 provider 可见前缀的代码前先读这三条，都有对应测试：

1. **激活只追加** — `Schemas()` 排序输出 core 工具，激活的 deferred 工具追加到
   **尾部**。排序插入会让其后所有 schema 位移，缓存从插入点失效。
2. **描述与 roster 解耦** — `tool_search` 的描述是静态的。roster 是动态的（MCP 陆续
   连上），只能走消息注入，进 prefix 就会 churn。
3. **默认惰性** — 不注册任何 deferred 工具时，`Schemas()` 行为与改动前逐字节一致。
   CLI 和现有 desktop 因此完全不受影响（`internal/boot` 缓存 golden 全绿验证）。

## 读写信息在哪

- **内核源码**【只读源】：`../internal/*`（87 个包），改动需评估对 CLI/desktop 的影响
- **本模块自有可写**：`desktop-lite/**`
- **根模块本次改动**：仅 `../internal/tool/{tool.go,deferred.go,search.go}` + 两个测试
- **配置**：复用 Reasonix 现有 `reasonix.toml` / `config.toml` 体系，本模块不另起炉灶
- **凭据**：走内核既有 provider 配置链路，**本模块不落任何密钥**

## GitHub 耦合

- **仓库**：`esengine/DeepSeek-Reasonix`（上游），fork 为 `SivanCola/DeepSeek-Reasonix`
- **分支**：`feature/desktop-lite`，基于 `origin/main-v2`
- **耦合深度**
  - 依赖 `reasonix/internal/*` 全部内核能力（agent / boot / provider / mcp / skill /
    checkpoint …）
  - 与 `desktop/` **无代码耦合**，仅共享内核；两者可同时安装运行
  - `internal/tool` 的改动是**共享面**：CLI、desktop、desktop-lite 三方共用，
    改它必须验证 `internal/boot` 缓存 golden
