---
owner: @esengine
backup: @SivanCola
status: active
reviewed: 2026-10-25
---

# Subagent collaboration / 子代理协作

## Guidance / 追加指导

Open a running subagent's full record in Studio and use **Additional guidance**.
Sending queues the text for the child's next processing boundary; it does not
interrupt a running tool or confirm that the model has read it. A rejected send
keeps the draft. A completed child cannot receive guidance from this form.

The existing session ownership checks and user-configured size/rate limits apply.

在 Studio 打开运行中子代理的完整记录，可通过「追加指导」补充要求。发送成功表示已排队，
不表示模型已读，也不会中断正在执行的工具。发送失败保留草稿；已结束的子代理不能通过此入口
接收指导。原有会话归属校验及用户配置的消息大小、频率限制继续生效。

## Reports to the parent / 向父代理报告

Ordinary delegated workers can call `reply_to_parent` with a `message`, then
continue their work. The runtime binds the sender and recipient; the tool has no
destination or authorization argument. Each run allows at most 32 reports of
16 KiB each.

Reports are agent-provided data, never user approval or a permission
grant. They do not replace the final result or a structured completion report.

Delivered host reports retain structured origin metadata in history, so their
addresses are not treated as URLs supplied by the user. Distinct reports keep
distinct inbox keys even if a provider reuses a tool-call ID in later rounds.

宿主报告在历史中保留结构化来源标记，报告里的网址不会被视为用户提供的网址。
即使厂商在后续轮次复用工具调用 ID，不同报告也有独立的收件箱去重键。

普通委派任务可调用 `reply_to_parent` 发送 `message`，随后继续工作。收发身份由运行时绑定，
工具不接受目标会话或授权参数。每次运行最多发送 32 条报告，每条最多 16 KiB。报告属于代理
提供的数据，不能充当用户批准或扩大权限，也不替代最终答案及结构化完成报告。

The root conversation uses its existing durable host inbox: reports join an
active turn or wake the idle conversation. Success acknowledges queue admission,
not model consumption.

Nested workers report only to their immediate delegating
agent while it accepts guidance. An unavailable receiver returns an error.

Ephemeral `read_only_task` runs retain their no-durable-side-effects contract and
return their final result instead of exposing this reporting tool.

根会话复用现有持久化宿主收件箱：报告进入正在执行的轮次，或唤醒空闲会话。成功仅确认入队，
不保证模型已处理。嵌套子代理只向直接父代理报告，父代理已结束时会拒绝。临时
`read_only_task` 保持不产生持久化副作用的约定，通过最终结果交付，不提供此报告工具。

## Selected context / 精选上下文

In a running child's Studio detail pane, expand **Hand off context**, enter a
summary and/or select completed results, then send. The host resolves references
in the current conversation lineage and workspace.

Full result snapshots are appended to the guidance; existing size/rate limits
apply to the expanded message. Oversized handoffs are rejected without truncation
and the draft stays editable. Use a shorter summary or fewer results.

This live handoff does not change the launch-time reader or tool definitions.
Older servers reject the dedicated `/subagents/{ref}/handoff` route rather than silently dropping
the context from a plain steering request.

The user's guidance text stays separate from selected result data for authority
checks. Opening context also stays outside raw input in both writer and read-only
tasks, including runs without a parent turn.

Model-originated `task_id` guidance also carries selected context and enforces
the delegating child's supplied-reference scope.

用户指导原文与所选结果数据在授权检查中保持分离。可写和只读任务的初始交接内容也不会
混入用户原文，包括没有父回合的运行。模型通过 `task_id` 追加指导时也会携带所选上下文，
并校验该子代理只能转交已提供给它的引用。

在运行中子代理的 Studio 详情中展开「交接上下文」，填写摘要、勾选已完成结果后发送。
宿主按当前会话谱系与工作区校验引用，将完整结果快照追加到指导消息。

现有大小及频率限制作用于展开后的消息。超限会拒绝而不截断，并保留可编辑草稿，
可缩短摘要或减少选择。
运行中交接不会改变启动时快照读取器或工具定义。

旧服务器对专用交接路由返回错误，不会把带上下文的请求当作普通指导成功接收后丢掉上下文。

The **Subagent reports** section reads report calls and their receipts from the
child's paged transcript, including after reopening it. The parent's live tool
cards also show child reports without opening raw arguments.

Queued means accepted by the delivery route, not read or approved by the parent.
Pending and failed sends stay distinct; report text is rendered as untrusted text.

「子代理报告」区从子代理分页记录读取报告调用与回执，重新打开后仍可查看。
父代理的实时工具卡片也直接展示子代理报告，无需展开原始参数。「已排队」只表示投递
路径已接收，不表示父代理已读取或批准；等待回执与发送失败分别展示，正文作为不可信
纯文本显示。

`task`, `read_only_task`, and runnable `fleet` items accept:

- `context_summary`: selected background and prior decisions, at most 16 KiB.
- `context_refs`: up to 64 completed `sa_...` results owned by this conversation
  lineage and workspace. Foreign or incomplete results are refused.

`task`、`read_only_task` 和 `fleet` 中实际运行的节点支持：

- `context_summary`：精选背景和此前已确认的决策，最多 16 KiB。
- `context_refs`：最多 64 个已完成的 `sa_...` 结果，必须属于当前会话谱系及工作区。
  其他会话、其他工作区或未完成的结果会被拒绝。

Fleet dependencies and explicit result references produce bounded previews in
the opening turn. `read_task_context(id, offset_bytes, limit_bytes)` reads the
complete supplied snapshot in UTF-8 pages, including material omitted from the
preview.

The reader cannot browse the parent conversation or arbitrary sibling
results. Nested delegation may forward only references already supplied to that
child. Each invocation has its own reader and message route.

A nested task without a selection starts without its parent's result context.
Forwarding uses the received snapshot, not a later version of the source result.

嵌套任务未选择上下文时不会继承父任务的结果；转交时使用已收到的快照，不读取源结果
后来新增的内容。

依赖结果及显式引用在任务开头提供有限长度预览。`read_task_context` 按 UTF-8 字节分页读取
本次交接的完整快照，包括预览省略的中间内容；它不能浏览父会话或任意兄弟代理的结果。
嵌套委派只能转交已提供给当前子代理的结果引用。每次调用独立绑定读取范围及消息路由。

Persisted context capsules record result references. A root-initiated compatible continuation
revalidates and reloads those references; it does not claim the workspace or the
source result is unchanged. Ephemeral results remain readable only during the
invocation that received them.

Older records without references remain readable;
continuation still requires compatible prompts, models, and tool schemas.

持久化上下文记录保存结果引用。根代理发起的兼容续接会重新校验并读取引用，不宣称代码或源结果保持
不变。临时结果只在收到它的本次调用内可读取。没有新增引用字段的旧记录仍可读取；续接仍
要求提示词、模型和工具定义兼容。

## Worktrees / 隔离工作树

Root `task` calls with `isolation: "worktree"` use the ordinary profile runner
inside a separately rooted kernel. Profiles, tool ceilings, declared write paths,
selected context, reports, transcripts and scheduler admission follow the same
delegation contracts.

Changes remain pending until `apply_isolated` or `discard_isolated` settles them.
Background execution retains this contract. Nested workers cannot create another
isolated kernel. A transcript cannot resume in a fresh worktree; select its result
as context for a new task instead.

根代理的 `task` 使用 `isolation: "worktree"` 时，在独立工作区内调用普通委派运行器，
统一遵守角色、工具上限、写入路径、上下文、报告、历史和调度约定。后台执行同样生效。
更改仍需通过 `apply_isolated` 或 `discard_isolated` 处理，不会自动写回源工作区。

嵌套代理不能另建隔离内核；旧历史不能在新建工作树中直接续接，可选择其结果作为新任务上下文。

## History and recovery / 历史与恢复

Studio opens the latest 100 messages and loads earlier pages on request, preserving
the viewport. Live polling appends new pages. A rewrite changes the snapshot identity
and reloads the latest page instead of joining different histories.

Graph recovery keeps dependency order distinct from actual context delivery.
Each completed execution records its result identity before releasing the transcript,
so reusing a member's transcript does not erase earlier execution outcomes.

Studio 首次打开只读取最近 100 条消息，按需加载更早页并保持滚动位置；运行中的消息继续轮询。
历史压缩或重写后会重新读取最新页，不拼接不同快照。任务图恢复区分依赖顺序和实际交接，
在释放历史前记录执行结果标识，成员续接不会覆盖此前执行的完成状态。

## Concurrency and spend / 并发与成本

- HTTP 429/503 retry signals halve future admissions. Successful completions
  restore capacity gradually, never above configured ceilings. Active write
  claims are retained. Unlimited mode is restored after pressure subsides.
- A task and its delegated execution branches share configured token, cost and
  wall-clock budgets, including background jobs and isolated workers.
- Usage already in flight and one tool-free finalization round can exceed a
  threshold. This is an observed-spend limit, not a prepaid reservation.
- Money uses fixed-point, currency-specific totals. Only the root budget's
  currency is compared with its cost limit; other currencies and missing usage
  cannot be treated as free or silently converted. Token budgets span models.

Shared admission accounting belongs to the current task scope. It does not pool
unrelated conversations or restore an in-flight budget after a process restart;
the existing session usage history remains separate from this runtime gate.

共享额度属于当前任务范围，不合并无关会话，也不在进程重启后恢复运行中的剩余额度；
已有会话用量历史与运行时额度检查保持分离。

- 429／503 重试信号会降低后续任务并发，成功完成后逐步恢复，不超过配置上限，不撤销现有写锁。
- 同一任务及其执行分支共享已配置的 token、费用和时长预算，包括后台和隔离任务。
- 已发出的请求与一次不执行工具的总结请求可能超出阈值；这是按已观测消耗收口，并非预付费额度。
- 费用按币种分别累计，仅比较根任务预算所用币种，不隐式换汇；跨模型可使用 token 预算。

## Named members / 具名成员

The root conversation can invoke `team` on demand:

| Action | Behavior / 行为 |
| --- | --- |
| `create` | Start a named task with an optional model and tool ceiling. 创建具名任务，可选择模型和工具范围。 |
| `list` | Read this conversation's members. 查看当前会话成员。 |
| `send` | Guide an active member or continue its compatible transcript. 向运行中成员追加指导，或续接兼容历史。 |
| `retire` | Retire a finished member while retaining history. 退役已结束成员，保留历史。 |

Membership uses persisted child records, not extra autonomous processes. Reports
go to the owning parent; it coordinates follow-up work. Children cannot manage
the roster. Reuse preserves model/tools and refuses incompatible prompt or schema
changes rather than widening authority.

成员信息使用持久化子代理记录，不额外启动常驻自治进程。报告交给所属父代理协调后续工作，
子代理不能管理成员目录。续接保持模型和工具范围；提示词或工具定义不兼容时明确拒绝。

## Compatibility / 兼容性

The optional transcript `snapshot` field is additive; old clients ignore it.
Old execution journals remain readable. New terminal records add an optional
`result` snapshot; older readers ignore it and may show less precise historical
graphs, but append-only writers do not rewrite existing records.

分页接口新增可选 `snapshot` 字段，旧客户端可忽略。旧执行日志仍可读取；新终态记录增加
可选 `result` 快照，旧版本忽略该信息，历史图可能不够精确，但追加式写入不会改写已有记录。

Members use `member` and `retired-member` values in the existing kind field.
Older clients can still read their ordinary transcripts without roster controls.

成员在已有 kind 字段使用 `member` 和 `retired-member`，旧客户端仍可读取普通历史，
但没有成员管理操作。

## Cache and permissions / 缓存与权限

Selected context and reports enter message history at turn boundaries. They never
rewrite the system prompt or embed task IDs, result bodies, or recipients into
tool schemas. New tool definitions can cause a one-time cold prefix after an
upgrade.

Neither selecting context nor exchanging reports widens the existing
tool, filesystem, approval, or review-authority grants. Full parent-history
forking is not introduced by these context fields.

精选背景和代理报告在轮次边界进入消息历史，不改写系统提示，也不把动态任务标识、结果正文
或收件人写入工具定义。升级新增工具定义可能造成一次缓存冷启动。上下文选择和报告交换均
不扩大工具、文件系统、审批或审查证明权限。这些上下文字段不会复制父会话全文。
