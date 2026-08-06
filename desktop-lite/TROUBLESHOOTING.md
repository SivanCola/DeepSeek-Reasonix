# TROUBLESHOOTING

> 只增不删，是历史账。踩了新坑追加；遇到似曾相识的现象，先翻这里。

## 1. `desktop/` 模块编译报 `undefined: repairWebKitSignalHandlers`

**症状**：`cd desktop && go build ./...` 报

```
./app.go:1019:2: undefined: repairWebKitSignalHandlers
./main.go:160:2: undefined: scheduleWebKitSignalHandlerRepair
```

**根因**：两层。`webkit_compat_linux.go` 是 cgo 文件（`#cgo linux pkg-config: gtk+-3.0`），
`CGO_ENABLED=0`（缺 gcc 时的默认）会整个排除它；而兜底文件 `webkit_compat_other.go`
带 `//go:build !linux`，在 Linux 上不生效，于是符号缺失。此外 Ubuntu 26.04 **只有
webkit2gtk-4.1**，而该文件默认走 `webkit2gtk-4.0`。

**修法**：

```bash
apt-get install -y build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev
cd desktop && go build -tags webkit2_41 ./...   # 4.1 必须带这个 tag
```

**避开**：`desktop-lite/internal/**` 刻意保持免 cgo，无头即可测；只有外壳链接原生工具包。

## 2. `go test -race` 报 `-race requires cgo`

**症状**：`go test -race ./...` 直接失败，提示需要 cgo；装了 gcc 前还会报
`C compiler "gcc" not found`。

**根因**：环境未装 gcc 时 `CGO_ENABLED` 默认为 0，而 `-race` 依赖 cgo。

**修法**：`CGO_ENABLED=1 go test -race ./...`（gcc 已装）。

## 3. `internal/agent` 的"读失败"类测试以 root 运行时必挂

**症状**：`TestMissingReasoningWarnStateReadFailureDoesNotOverwriteExistingIncidents`
报 `new incident was unexpectedly persisted from a partial read`。

**根因**：**不是代码 bug**。该测试用 `os.Chmod(path, 0)` 模拟读失败，但 root（uid 0）
被内核豁免权限检查，读依然成功，测试前提不成立。在容器或 CI 里以 root 跑测试就会踩到。

**修法 / 避开**：换非 root 用户跑该包。判断某个失败是否属于此类，一句话可验：

```bash
D=$(mktemp -d); echo x > $D/f; chmod 0 $D/f; cat $D/f   # root 下仍能读出内容
```

## 4. 多个会话/工具共用同一个 checkout，改动会互相覆盖

**症状**：`git status` 突然变干净、HEAD 莫名前进；自己没提交却多出提交。

**根因**：同一个工作区被多个并行的会话或自动化工具共用，彼此的 stage/commit 互相影响。

**修法 / 避开**：开新功能一律用 **worktree** 隔离：

```bash
git worktree add -b feature/xxx ../DeepSeek-Reasonix-wtN origin/main-v2
```

⚠️ `git worktree add ... origin/main-v2` 会把 upstream 设成 `origin/main-v2`
（受保护分支），务必立刻 `git branch --unset-upstream`，否则误 `git push` 有风险。

## 5. 往 Registry 中途 `Add` 工具会打掉 prompt 缓存

**症状**：会话中途启用能力（如 economy 模式的 `connect_tool_source`）后，后续请求
缓存命中率骤降。

**根因**：`Registry.Schemas()` 把工具名**排序后全量输出**。中途 `Add` 一个名字排在
中间的工具，会让其后所有 schema 位移；而工具 schema 位于 provider 可见前缀最前端，
于是缓存从插入点起全部失效。`internal/boot/token_profile.go:298` 的 `addTools` 正是
用 `reg.Add()`，属于此形状。

**修法**：用 deferred 分层的 `AddDeferred` + `Activate`——激活的工具**追加到列表尾部**，
增长严格 append-only，激活前缓存的前缀继续命中。回归测试见
`internal/tool/deferred_test.go: TestActivateAppendsToTailPreservingCorePrefix`。

## 6. 持注册表锁调用工具回调 = AB-BA 死锁

**症状**：注册表读操作与 MCP 懒加载并发时挂死。

**根因**：lazy MCP placeholder 在工具回调（如 `ReadOnly()`）里拿 spawn 锁，而该 spawn
的 `trySwap` 需要注册表写锁。若读方持注册表读锁跨越工具回调，即构成 AB-BA。仓库的
`ContractEntries` 曾因此挂过，已修。

**修法**：锁内只快照 `Tool` 句柄，**解锁后**再调 `Description()` / `ReadOnly()` 等回调。
`DeferredRoster` 按此写法实现，回归测试见
`internal/tool/deferred_test.go: TestDeferredRosterDoesNotHoldRegistryLockAcrossToolCallbacks`。

## 7. Wails 应用启动即退/报 "will not build without the correct build tags"

**症状**：编译成功，运行后日志只有一行
`Wails applications will not build without the correct build tags.`，窗口不出现。

**根因**：Wails v2 在运行期检查构建 tag，普通 `go build` 缺 `desktop` 和 `production`。

**修法**：

```bash
go build -tags "desktop,production,webkit2_41" -o reasonix-lite .
```

（`webkit2_41` 是本项目在 webkit2gtk-4.1 环境下的额外要求，见第 1 条。）

## 8. `vite build` 删掉 `dist/.gitkeep`，新克隆编译不过

**症状**：构建前端后 `git status` 多出 ` D frontend/dist/.gitkeep`；在没跑过前端构建
的新克隆上编译 Go 报 `pattern all:frontend/dist: no matching files found`。

**根因**：`main.go` 用 `//go:embed all:frontend/dist` 嵌入前端，要求该目录存在，所以
仓库里放了 `dist/.gitkeep` 占位；而 vite 的 `emptyOutDir: true` 每次构建都会清空整个
目录，把占位文件一并删掉。

**修法**：保留 `emptyOutDir: true`（否则旧 hash 产物会堆积），在 build 脚本末尾把占位
文件写回：

```json
"build": "tsc --noEmit && vite build && node -e \"require('fs').writeFileSync('dist/.gitkeep','')\""
```

**顺带**：改了前端**必须先 `pnpm build` 再编译 Go**，否则嵌进二进制的是旧产物——
这类问题表现为"改了界面没生效"，很容易误判成缓存或前端 bug。

## 9. 会话被替换后，旧轮次的完成会污染新会话状态

**症状**：用户在一轮对话进行中切换项目，新会话看似"卡住"或反过来允许并发两轮。

**根因**：`Send` 不能持锁跨越 `Run`（一轮可能几分钟），解锁期间 `Open` 可能已替换会话；
旧轮次返回后若无条件清 `running`，清掉的是**新会话**的标志。

**修法**：generation 计数器。`Send` 开始时捕获 generation，`Run` 返回后只有 generation
未变才清标志；`Open` 每次成功都自增。实现见 `internal/session/session.go`，回归测试见
`TestReplacedConversationCompletionDoesNotClearNewRunningState`（已验证：拆掉守卫即红）。
