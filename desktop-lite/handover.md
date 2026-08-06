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
# 内核层（免 cgo，无头可测）
cd desktop-lite && go test ./...
cd desktop-lite && CGO_ENABLED=1 go test -race ./...

# 现有 desktop 模块（仅有 webkit2gtk-4.1 的环境需带 tag，见 TROUBLESHOOTING）
cd desktop && go build -tags webkit2_41 ./...
```

## 进度

**已完成**

- `internal/tool` deferred 分层（在**根模块**，非本模块）
  - `deferred.go`：`AddDeferred` / `Activate` / `DeferredRoster` / `PinPrefix` /
    `RenderDeferredRoster` / `Availability`
  - `search.go`：`tool_search` 工具，支持 `select:名字`、关键字、`+必需词`
  - `tool.go`：Registry 分层字段、`Schemas()` 追加式尾部、抽出 `addLocked`
  - 21 个测试，锁死三条缓存不变量（见下）
- `desktop-lite/internal/session`：单会话运行时 `Host`，11 个测试（含 `-race`）
  - generation 守卫防 stale completion；已验证拆掉守卫测试即红
- 服务器构建工具链：gcc / pkg-config / gtk+-3.0 / webkit2gtk-4.1 已装

**进行中**：无

**待办**（按建议顺序）

1. **把 deferred 接进真实会话** — 需要给 `control.Controller` 加 `*tool.Registry`
   访问器（目前只有内部字段 `controller.go:458`，无导出方法）。接上后：MCP/skill
   工具走 `AddDeferred`，core 加 `tool_search`，roster 用 `RenderDeferredRoster`
   注入首轮消息。
2. **Wails 外壳** — `main.go` + 薄绑定层，只做窗口和 IPC，业务留在 `internal/`。
3. **前端** — transcript + composer + ⌘K 命令面板三块。目标 ~4000 行 TSX。
4. **配置推导** — 继续读同一份 `config.toml`（兼容），UI 只暴露 provider + 凭据，
   其余全部计算默认值。现有配置有 246 个 TOML 字段，目标暴露面 5–8 项。

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
