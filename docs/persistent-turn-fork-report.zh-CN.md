# 持久轮次分叉实施报告

日期：2026-09-14
基线：`main-v2` 的 `09cdab386`

已完成的轮次现在可以分叉为独立的子会话：父会话继续运行时分叉、父会话只读时分
叉，以及重启之后分叉。判定“该轮次已结束”的依据是持久化的事件日志，
绝不是 checkpoint：checkpoint 继续只负责自己的文件回滚职责。

## 已交付能力

- **轮次边界来自日志。** 会话投影为每个已完成轮次记录两个事实：`MessageID`，
  即该轮次最终文本回复在 transcript 中的稳定身份；`BoundarySequence`，即结束
  该轮次的提交的最后一个 sequence。只有当终结性的 `turn/end` 关闭了该轮次
  时，该轮次才可分叉。完成状态绝不从回答文本、经过时间或运行状态推断。
- **切分覆盖整个提交。** `BoundarySequence` 是收尾提交的最后一个 sequence，
  而不是 `turn/end` 事件的 sequence。轮次结束与随之结束的状态可以共处同一个
  提交，若在事件处切分，要么会继承半个操作，要么会被安全前缀检查拒绝。
  这两种拒绝各自保留自己的类型化原因（`ErrForkActiveAuthority`、
  `ErrForkBoundaryNotAtomic`），并且绝不会为了迁就而裁剪提交。
- **所有读取路径共用一次目标查询。** `ForkTarget` / `ForkTargetSet` /
  `ForkTargets` / `ForkSequence` 只依据已提交的事件作答，因此活跃的源、
  被其他进程占用的源，以及对已关闭会话的冷读会产生相同的目标。未结束的轮次以
  `available: false, reason: turn_open` 列出，正是这一点让界面可以只禁用该轮
  次，而不是在会话运行期间禁用所有轮次。
- **旧历史被拒绝，而不是被猜测。** 只保留消息、没有轮次记录的源会报告
  `verifiable: false` 和空目标列表。
- **只读源和冷源可以分叉。** `Session.Fork` 不再拒绝没有持有租约写者的会话，
  冷读句柄会暴露自己的目录，因此子会话无需取得父会话的写者租约即可复制父会话
  持有的文件。被其他进程占用的父会话保留自己的租约和正在运行的任务。
- **创建与导航分离。** `Service.CreateFork` 发布子会话并返回其身份，
  不打开 runtime、不切换 controller，也不写入父会话。
  `Controller.CreateForkSession` 不接受 rotation gate，因此正在运行的父会话
  继续运行。桌面端随后在新标签页中打开子会话；当这次 attach 失败时，
  结果仍带有子会话 id 和一个可恢复错误，且子会话绝不会被删除。
- **一个操作 id 对应一个子会话。** 重试的创建请求指向同一个子会话；
  不同的操作 id 会创建另一个子会话。
- **远端创建不接管父会话。** `GET /fork-targets` 和 `POST /fork-session` 只
  做创建，不触碰前台会话、广播绑定和租约。它们以 `session-fork-targets-v1`
  对外声明；连接到不支持该能力的服务器的桌面端会报告服务器不受支持，
  而不会回退到 `/fork`。
- **按钮跟随持久化日志，而不是 checkpoint。** transcript 通过稳定的消息身份
  把轮次匹配到它的目标（用 `ForkTargetView.messageId` 对比渲染出的回答的消息
  id），因此实时完成、分页历史和冷恢复共用同一套映射。分叉不再读取
  checkpoint 或会话级的运行标志；轮次运行期间，只有未结束的那个轮次保持不可
  用。checkpoint 仍然只驱动文件 rewind。
- **每种拒绝都会说明自身。** 分叉入口会报告自己的状态——轮次未结束、
  目标仍在加载、边界无法确认、服务器不受支持、创建进行中——并以英文、
  简体中文和繁体中文完成本地化；失败会进入既有的通知通道，而不是被吞掉。
- **旧路径行为不变。** `Fork`、`ForkForTab`、`ForkWorktreeForTab`、
  `ForkRemoteTab`、`POST /fork` 以及所有 rewind scope 都保留原有语义和
  rotation 保护，包括脏工作区检查。会切换会话的那些分叉命令仍然对 CLI 和旧客
  户端可用，即使桌面端 UI 已不再调用它们。

## 兼容结论

没有任何持久化字节发生改变。v4 日志、manifest、帧编解码和内容存储均未改动，
子会话以当前格式写入。

| 字段或格式 | 旧数据行为 | 新读者行为 | 旧读者行为 | 结论 |
| --- | --- | --- | --- | --- |
| `events.frames`、`manifest.json`、帧、`.content-v1` | 不变 | 照常读取 | 能读取新版写入 | 无格式变化 |
| `Projection`、`TurnBoundary`（+`messageId`、+`boundarySequence`） | 由已提交事件重新计算 | 计算新字段；无迁移、无 checkpoint 回填 | 不适用——从不持久化 | 安全 |
| Host RPC 契约 | 增量添加 | 4 个新命令、3 个新接口 | 旧前端忽略它们 | 安全 |
| Serve 能力集 | 增量令牌 | 声明 `session-fork-targets-v1` | 旧桌面端使用 `/fork` | 安全 |
| Checkpoints（`.ckpt/` sidecar） | 不变 | 不变；rewind 仍使用它们 | 不变 | 安全 |

host 契约的差异是增量的：`desktopContract.generated.json` 154 处新增 / 0 处
删除，`host_command_owners.generated.json` 30 / 0。
`desktopContract.generated.ts` 中唯一的删除是契约摘要行，该行本就应随契约变
化。

测试期间发现的既有不一致，不是本次引入，也保持原样：`projectLegacyImport` 在
`legacy/import` 上接受 `source` 字段，但 `internal/session/history_index.go`
在 `DisallowUnknownFields` 下解码同一事件时只接受 `messages`，因此携带
`source` 的导入事件会使历史索引重建失败。唯一的生产写入方只输出
`{"messages": …}`，所以当前没有任何路径会触发它；将来若有写入方加上
`source`，就会破坏冷历史分页。

## 缓存契约

`scripts/check-cache-impact.sh` 报告 **"No cache-sensitive prompt/tool files changed."**
没有任何对 provider 可见的提示、记忆前缀、工具 schema 或序列化被改动，
因此不适用任何缓存命中警告。

新的投影字段不属于 `provider.Message`，`ModelMessages` 的构造也未改变；
`TestProviderRequestBytesSurviveSessionV4RoundTrip` 通过。子会话通过目标边界
继承完全相同的事件前缀，因此它的模型上下文就是父会话在该边界处的投影，
并且没有任何 UI 锚点、禁用原因或操作 id 进入模型消息。

本报告不声明子会话首次请求的缓存命中率影响。经证实的结论更窄：父会话的请求字
节不变，且子会话继承的前缀等于父会话在目标边界处的投影。

## 验证证据

以下命令在工作树中独立重跑，而不只是由实施智能体报告：

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/session/ -count=1` | ok（32s） |
| `go test ./internal/session/ -run 'ForkTarget\|CreateFork' -race -count=1` | ok，9/9 |
| `go test ./internal/session/ -run 'Cache\|Provider' -count=1` | ok |
| `go test ./internal/control/ -count=1` | ok（200s） |
| `go test ./internal/serve/ ./internal/servecontract/... -count=1` | ok（67s） |
| `cd desktop && go test -run 'ForkTargets\|CreateFork' -count=1 .` | ok |
| `go build ./internal/... ./cmd/...` | ok |
| `go run ./tools/repolint` | clean |
| `scripts/check-cache-impact.sh` | clean |
| `cd desktop && go test -run 'HostContract\|HostCommandOwners\|HostShellRemote' -count=1 .` | ok |
| `cd desktop && go test -count=1 .` | ok（192s） |
| `cd desktop/frontend && pnpm typecheck` | ok |
| `tsx src/__tests__/turn-fork-transcript.test.tsx` | ok |
| `node scripts/run-tests.mjs --keep-going`（frontend） | 352 个 test suite 全部通过 |
| `en.ts` / `zh.ts` / `zh-TW.ts` 之间的 locale 一致性 | 9 个新 key 在每个文件中各出现一次 |
| `node bench/fork-targets.mjs`（Chromium，真实 Transcript） | PASS，16 次连续运行 |
| `node bench/fork-targets-app.mjs`（已构建应用，`/?mock=1`） | PASS，5 次连续运行 |

浏览器 bench 针对真实的 `Transcript` 运行，使用隔离的 fixture 数据，
并读取渲染后的 DOM，而不是内部状态：

- 已完成的轮次渲染出可用入口：不含 `aria-disabled`，tooltip 和 `aria-label`
  在英文下为 "Branch into a new conversation"，在简体中文下为
  "在新对话中分支"。
- 未结束的末尾轮次渲染为 `aria-disabled="true"`，原因提示为
  "This turn has not finished yet, so it has no boundary to branch from."
  （"该轮次尚未结束，还没有可供分支的边界。"）。
- 历史中不保留轮次记录的源会把边界渲染为无法确认
  （"该轮次在会话记录中没有可确认的分支边界。"）。
- 点击会派发目标稳定的 `turnId`，两次点击携带两个不同的操作 id。
- 在已构建应用中，一次点击会切到子会话标签页；把 fixture 强制为 attach 失败
  时，通知会指明已创建的子会话，并且**第二次点击仍指明同一个子会话**——
  两次点击只创建一次。

会话测试覆盖：controller 已不存在、会话以只读方式重新打开后仍列出已完成的轮
次；冷分叉只继承到目标轮次为止的前缀；末尾轮次未结束时更早的轮次仍可分叉；
未知轮次 id 被拒绝，而不是重定向到最新轮次；切分覆盖结束该轮次的整个提交；
按操作 id 幂等重试；只读源产生可写子会话且源日志不变；提交后执行授权仍未关闭
的边界以自己的原因被拒绝，并且不发布任何内容；以及只保留消息的历史被报告为无
法确认。

## 已知缺口

- **恢复入口是通知，不是控件。** 当子会话已创建但其标签页无法打开时，
  子会话会被保留并指明，对同一轮次目标的第二次分叉会复用它，而不是再创建一个
  （已在已构建应用中验证：两次点击只创建一次）。前端没有按会话 id 打开的路径
  ——会话恢复基于路径——因此恢复以指向会话历史的通知形式出现，与既有的
  `rewindForkAttachError` 约定一致，而不是一个可点击并重新打开子会话的入口。
- **"没有权限创建子会话"不是一个可预判的状态。** 计划把它列为分叉入口必须展
  示的具体原因之一。只读源是被有意设计为可分叉的——子会话从源写出，
  绝不写入源——因此前端没有任何只读信号能在不破坏该要求的前提下禁用入口。
  本地化字符串和原因槽位都存在，但今天没有任何东西会产生它们：一次拒绝（镜像
  的前台会话、未认证的远端、创建失败）经错误通道到达，并以其自身文本展示。
  把入口接到通道会话的只读标志上，会错误地禁用一个必须成功的分叉。
- **在另一个 rewind 正在提交时请求的分叉，不再被 UI 阻塞。** 这是去掉会话级
  禁用后的预期结果；改由 host 以自己的原因拒绝它。
- 携带 `source` 字段的导入 `legacy/import` 事件会使历史索引重建失败（见兼容
  结论）。这是既有问题，当前没有任何写入方会触发。

## 明确未包含的证据

- **浏览器中的远端分叉。** 远端创建路径仅由 node 测试覆盖（调用形式、
  新的操作 id、复用未打开的子会话）。没有任何浏览器运行覆盖它，因为那需要一
  个可 attach 的实时 Serve 界面。
- **两个 bench 门禁没有接入 `package.json`。** 它们可以用上面的命令运行，
  但尚未在 CI 中运行，因此没有任何机制能阻止它们失效。
- **打包的桌面端和原生 shell。** 没有构建、签名或启动任何包，因此生产模式下
  的 shell/service 启动未经验证。
- **Windows 和 Linux。** 上面所有命令都只在 macOS/arm64 上运行过。
- **真实 provider 调用。** 没有进行任何真实 API 运行；子会话继承的上下文是依
  据持久化投影验证的，而不是依据 provider 验证的。
- **竞争条件下的跨进程租约行为。** 只读路径被设计为不加锁，测试也覆盖了关闭
  后的冷读，但没有任何测试驱动两个活跃进程争用同一个会话。

## 值得保留的 fixture 发现

bench 中的 locale 切换最初按同步方式断言，大约每七次运行就会失败一次。
原因是应用自身的特性，与本次分叉工作无关：`src/lib/i18n.tsx` 按需加载 locale
词典，`translate` 在该 chunk 解析完成前回退到英文，因此切换后的首次渲染显示
英文是合理的。十次运行测得的生效延迟为 12-98 ms，因此没有任何内容丢失或卡住
——页面本来就没有承诺同步切换。bench 现在改为等待渲染出的值
（`page.waitForFunction`，10 s 上限），而不是 sleep，并已连续通过 16 次运
行。今后任何读取本地化文本的浏览器检查都必须照此处理。
