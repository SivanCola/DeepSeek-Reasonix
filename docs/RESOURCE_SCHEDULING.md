# Resource admission and development process ownership / 资源准入与开发进程归属

Reasonix shell tools share a CPU/memory admission budget per OS user, independent
of chat, workspace, worktree or `REASONIX_HOME`. Foreground tools, persistent
shells, host terminals, background jobs and user `!` commands acquire before
launching. Queued work is cancellable; cancelling a queued job never starts it.
Light diagnostics and recognized development servers remain available; unknown
scripts are conservatively heavy.

Reasonix 的重型命令共享用户级 CPU／内存预算，不随聊天、工作目录、worktree 或
`REASONIX_HOME` 分裂。前台、持久 Shell、宿主终端、后台任务及用户 `!` 命令
在启动前准入；排队可取消，取消后不会启动命令。轻量诊断和已识别的开发服务器
可继续运行，未知脚本默认按重型任务处理。

This is cooperative admission, not an OS CPU/RSS quota. Memory is an estimated
reservation, not measured RSS or a process memory limit. Explicit worker
overrides, parallel descendants and unrelated applications can exceed it.
No daemon, administrator permission or platform quota service is required.

这是协作式准入，不是操作系统的 CPU／RSS 硬限额。内存是预计预留量，不代表
实测 RSS，也不会强杀超量进程；显式并发覆盖、任务内并行子进程及其他应用仍可能
突破预留。无需常驻服务、管理员权限或平台配额服务。

## Budget and fairness / 预算与公平性

Defaults reserve half the logical CPU cores (minimum one) and half physical RAM
for all checks. Each task requests the whole CPU budget and at most 2048 MiB,
preserving conservative one-large-check admission without setup. Smaller default
task reservations allow checks to run together. Go package and per-process
parallelism jointly fit the CPU request; Vitest 4+ and legacy pools, Cargo,
CMake and CTest receive bounded worker variables. Smaller explicit limits remain.
Vitest names follow its [official migration guide](https://v4.vitest.dev/guide/migration.html).

默认总预算为逻辑核心数的一半（至少一个）及物理内存的一半。每项任务默认申请
全部 CPU 预算和最多 2048 MiB，保持无需配置时大型检查的保守准入；调小默认任务
预留即可并行运行多项检查。Go 包并发与每进程并发共同受 CPU 申请量约束；
Vitest 新旧池、Cargo、CMake、CTest 接收 worker 参数，并保留较小的用户限制。

A short OS lock serializes registration/admission. Monotonic tickets order
registered waiters FIFO. Only the first waiter can claim available CPU and
memory; later small tasks cannot backfill ahead of it. This prevents starvation
at the cost of spare capacity temporarily idling behind a large head request.
Each task owns a random OS-locked lease; a crash releases the lock and the next
snapshot reclaims its entry. Liveness never depends on PID reuse, heartbeat or
expiry. Nested runners reuse one parent reservation, without deadlocking or
counting twice. Lease release is idempotent.

短时文件锁保护注册和准入，活动等待者按单调序号 FIFO 排队。只有队首能申请
剩余 CPU 和内存，后来的小任务不能插队；这样避免饥饿，代价是队首大任务等待时
可能暂时闲置部分容量。每项任务持有随机文件锁租约，崩溃由操作系统释放，
下次快照回收记录，不按 PID、心跳或到期时间判断存活。嵌套检查复用一个父任务
预留，避免自锁和重复计数，释放幂等。

Descendants inherit the host scope separately from private cache settings and
probe leases read-only, so workspace-write sandboxes need no extra cache writes.
Never unlink permanent `heavy.lock` or `queue-v2.lock` while participants run.
Persistent POSIX shells clamp workers before each admitted command without
resetting cwd/exports. Smaller limits remain and are not raised automatically.
That path uses Go package parallelism one to avoid multiplying process limits.

子进程单独继承宿主资源域，不随私有缓存路径改变；租约只读探测不要求沙箱额外
写权限。任务仍可能运行时，不可删除固定的 `heavy.lock` 和 `queue-v2.lock`。
持久 POSIX Shell 在每条准入命令前收紧 worker 参数，不重置目录和导出变量；
保留较小的限制且不自动调高。该路径使用一个 Go 包 worker，避免与进程并发相乘。

## Configuration and wait visibility / 配置与等待原因

```sh
# Show policy path, reserved resources and running/queued task counts:
node scripts/resource-run.mjs --status
# Example: host with at least 8 logical CPUs and 8 GiB RAM.
# Up to two default 4-CPU tasks, each reserving 2 GiB:
node scripts/resource-run.mjs --configure --cpu-budget 8 --memory-budget-mib 8192 --cpu 4 --memory-mib 2048
# Override one known task, leaving the shared policy unchanged:
node scripts/resource-run.mjs --cpu 2 --memory-mib 1024 -- go test ./internal/resourcebudget
```

The shared `budget-v2.json` lives in the host scope printed by `--status`, outside
project/Reasonix home. Updates are atomic. Values must be positive and fit this
host; a request must fit the budget or fails immediately. Reductions do not
preempt existing owners: new admissions wait for reservations to drain. A queued
request made impossible by a reduction fails explicitly. Unknown policy fields
and live queue formats fail closed rather than silently ignoring future rules.

共享 `budget-v2.json` 位于 `--status` 显示的宿主资源域，与项目、Reasonix home
无关，配置原子更新。数值须为正且在宿主容量内，任务预留须在预算内，否则立即
失败。缩小预算不抢占运行任务，新任务等旧预留释放；缩小后无法满足的排队请求
明确失败。未知配置字段或活动队列格式报错，避免默默忽略新策略。

Foreground progress, background output and the external runner report queue
position, requested/reserved resources and whether FIFO order, CPU, memory or a
legacy owner blocks admission. Notifications update only on changes, outside
scheduler locks. `--status` reports reservations, not measured load.
Per-task environment hints `REASONIX_TASK_CPU` and `REASONIX_TASK_MEMORY_MIB`
also work; nested runners keep the parent's reservation.

前台进度、后台输出和外部入口显示队列位置、申请／已预留资源，以及 FIFO 顺序、
CPU、内存或旧版占用等原因；仅在信息变化时更新，回调不占调度锁。`--status`
展示预留量而非实测负载。也支持单任务环境变量 `REASONIX_TASK_CPU` 和
`REASONIX_TASK_MEMORY_MIB`；嵌套执行始终沿用父任务预留。

## External checks / 外部检查

Standard `make` build/vet/test targets and main Desktop `pnpm` build, typecheck,
test/full-suite scripts automatically join local admission, including when
external agents invoke them. Nested scripts share one reservation.
`CI=true`/`CI=1` automatic package scripts and Make targets retain CI job/shard
budgets; an explicit wrapper still admits in CI. Ad-hoc heavy commands use:

常用 `make` 构建／vet／测试及 Desktop 主 `pnpm` 构建、类型检查、测试／全量
脚本自动参与本机调度，外部 Agent 调用时也一样；嵌套脚本共享一个预留。
`CI=true`／`CI=1` 时自动入口沿用 CI 分片预算；显式 wrapper 即使在 CI 也准入。
外部临时重型命令使用：

```sh
node /absolute/checkout/scripts/resource-run.mjs -- go test ./...
# Independent Desktop module, without expensive test-input trace hashing:
cd desktop
node ../scripts/resource-run.mjs -- go test -count=1 -timeout=25m ./...
```

The small Go helper boots no application/provider state and preserves caller cwd,
streams and exit code. It requires the repository Go toolchain. Direct commands
from arbitrary applications cannot automatically be scheduled: no global
shell/PATH hooks or process-name killers are installed. Use standard repository
targets or the explicit wrapper for those commands. The Desktop full suite uses
`-count=1` to avoid enormous result-cache input traces while retaining compiler
cache and executing every test; 25 minutes is its existing module-wide budget.

轻量 Go 入口不初始化应用或供应商状态，保留目录、标准流和退出码，需要仓库 Go
工具链。其他应用直接启动的任意命令无法自动纳入调度；本实现不安装全局
Shell／PATH 钩子，也不按进程名称杀任务，应使用标准仓库目标或显式 wrapper。
Desktop 全量使用 `-count=1` 避免巨量测试结果缓存输入日志哈希，保留编译缓存并
执行全部测试；25 分钟为仓库既有模块预算。

## Electron output and cleanup / Electron 输出与回收

Development logs and service stderr share a guarded echo sink. Both synchronous
write failures and asynchronous stream errors disable echo permanently; file
logging continues. Backpressure drops optional echo until drain rather than
buffering indefinitely. The exception logger cannot recursively write into a
broken stderr pipe. Development launchers route termination and hold an
authenticated local ownership connection. A forcibly killed launcher closes
that connection at the OS boundary; its shell saves drafts, shuts down the Go
service, awaits asynchronous browser/resource cleanup, and exits. Intentional
relaunch removes the old launcher binding. Direct development/test launches
without that endpoint also monitor parent exit/reparenting as a compatibility
path; managed connection ownership is preferred. Packaged applications are not bound
to the lifetime of a development launcher. Save failures still withhold exit
under the existing data-preservation contract.

开发日志和服务 stderr 共用受保护的回显通道：同步写失败或异步流错误都会关闭
回显，文件日志继续；背压期间丢弃可选回显，避免无限缓冲。异常处理不会再次向
已断开的 stderr 写入。开发启动器转交终止操作，并通过经过认证的本地连接持有
进程归属；启动器被强杀后，操作系统断开连接，Shell 保存草稿、关闭 Go 服务、
等待浏览器及其他异步资源清理后退出。直接启动的开发／测试实例会兼容检测父进程
退出或重挂，优先使用有连接归属的启动器。主动重启解除旧启动器绑定，发行版不绑定
开发启动器生命周期。保存失败时仍按既有数据保护约定保留退出阻断。

## Regression checks / 回归验证

```sh
go test -race ./internal/resourcebudget ./internal/shellrun ./internal/tool/builtin
node --test scripts/resource-run.test.mjs
cd desktop/electron
pnpm typecheck
pnpm test
# Requires built frontend/dist and desktop/build/bin/reasonix-desktop-service:
pnpm test:process-lifecycle
```

The native smoke uses real Electron and the real Go service with disposable
homes and no provider requests. It breaks stderr, terminates the launcher with
SIGTERM and SIGKILL (including startup cancellation), checks ordered cleanup
logs and verifies both PIDs exit. Pre-hello cancellation closes the uninitialized
service transport; established runtimes still require confirmed shutdown.
Run without Playwright/Node inspector when asserting actual process exit.

原生回归使用真实 Electron 与 Go 服务、临时数据目录，不调用供应商。它断开 stderr，
分别对启动器发 SIGTERM 和 SIGKILL（包含启动期间），检查清理顺序与两个进程的退出。
hello 尚未完成时关闭未初始化的服务传输；已初始化的运行时仍须确认安全关闭。验证真实退出
时不可附加 Playwright／Node 调试器。

## Compatibility / 兼容性

Versioned policy/queue files coexist in `resource-budget-v1`; session files,
config.toml, Desktop RPC, tool schemas and provider construction do not change.
V1 takes the permanent `heavy.lock` exclusively; V2 running tasks hold it shared.
An old task runs alone, or weighted tasks run together: they cannot overlap.
Old nested runners still validate unchanged nonce leases. V1 cannot honor
weighted policy or FIFO; update all participants for those guarantees.
Pre-budget builds remain outside admission. Unsupported live formats fail
closed; downgrade does not rewrite user sessions or provider data.

版本化预算／队列文件位于原 `resource-budget-v1`，不修改会话、config.toml、
Desktop RPC、工具 schema 或供应商请求。V1 独占固定 `heavy.lock`，V2 运行任务
共享持有：旧任务独占与多项加权任务互斥，不会重叠。嵌套旧入口仍可验证原 nonce
租约；V1 不支持加权策略或 FIFO，需全部更新才能保证这些行为。更早无预算的
版本不受控。未知活动格式明确失败，降级不改写用户会话或供应商数据。
