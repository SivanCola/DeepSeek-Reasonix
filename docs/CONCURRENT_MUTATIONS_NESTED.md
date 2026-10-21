---
owner: @SivanCola
backup: @esengine
status: active
reviewed: 2026-10-22
---

# Nested isolation result management / 嵌套隔离结果管理

## 中文

本页记录嵌套结果管理层的实现，不代表生产嵌套 worktree 已启用，也不改变新安装默认策略。
总体边界见[并发变更说明](CONCURRENT_MUTATIONS.md)和[协议矩阵](CONCURRENT_MUTATIONS_PROTOCOL.md)。

根会话的隔离成果列表沿已登记的直接父子关系发现保留的子成果及其整合尝试。
未登记的候选不会因共享存储目录而获得管理权限。列表显示直接父任务及实际整合目标；
子成果的目标是其直接父环境。此管理路由只提供查询和明确预览后的丢弃，不授予向祖先写入的权限。

父记录终态迁移时允许同一候选目录的旧 marker 别名，保持子成果的不可变反向关联。
ID、generation 和 token 仍须匹配；运行中的父记录不接受该迁移别名。

同 ID 重复、反向关联不匹配、未知版本、坏 JSON 或非普通记录文件会保留现场并拒绝管理。
丢弃仍绑定完整预览版本；候选变化后需要重新预览。清理子成果不会修改任何祖先工作副本。

嵌套成果首次准备整合时，在同一 child entry 锁内核验原始清单和执行身份、持久发布 v5 屏障，
再登记独立的 int 尝试。登记拒绝后，失败收尾不能绕过该边界补建记录。
屏障与首条 int 之间中断时，child 仍可被发现和处理，不会仅因 v5 永久占用父环境。

即使 child 已丢弃或整合，其尚未结算的 int 仍保留直接父环境。只有关联尝试全部处于终态、
完成清理且释放物理 OS owner，父任务才可换代或清理。child 自身续跑也不能跨越待处理的 int。
旧 generation 的已结算收据可以保留，不阻止之后合法的续跑。

普通持久化和父子关系更新保留 v5 及未知字段。已验证的先前 v4 实现拒读/拒写该版本。
版本屏障不能撤销已经越过旧版快照阶段的在途直接 Store API 调用；不将其宣传为完整跨版本 fencing。
旧生产入口尚未开放嵌套 worktree，解除限制前仍需生产装配、恢复和跨平台资格验证。

## English

This page records the nested management owner. It neither enables production
nested worktrees nor changes installation defaults. See the
[concurrency guide](CONCURRENT_MUTATIONS.md) and [protocol matrix](CONCURRENT_MUTATIONS_PROTOCOL.md).

The root catalog follows registered direct-parent edges to retained descendants
and their integration attempts. Sharing a storage root grants no authority over
unregistered candidates.

Each descendant displays its direct parent and actual integration target.
This route supports observation and explicitly previewed
discard, without granting source-write authority into an ancestor.

Terminal marker promotion accepts only the old marker in the same candidate
directory, preserving immutable child backlinks. ID, generation and token must
still match. Running parents do not accept this promotion alias.

Duplicate IDs, mismatched backlinks, unsupported versions, malformed JSON and
non-regular markers retain state and refuse management. Discard remains bound to
the complete preview revision. Cleaning a descendant never writes ancestor files.

Initial nested integration registration rechecks the immutable manifest and
execution identity under the child entry lock, durably publishes a v5 barrier,
then creates the int record. Rejected registration cannot publish a record from
failure cleanup.

Interruption between the barrier and initial record leaves a
discoverable child whose explicit settlement can still release its parent.

Even a terminal child pins its direct source while an associated int is pending,
uncleaned or physically owned. Parent generation changes and cleanup wait until
all attempts settle. Child resume also refuses pending attempts; retained settled
receipts from earlier generations do not prevent a later valid resume.

Ordinary persistence and lineage updates preserve v5 and extension fields. The
qualified earlier v4 implementation refuses this boundary.

A version upgrade
cannot revoke an old direct API preparer already past its snapshot; complete
fencing of that unsupported in-flight call is not claimed. The previous
production nested guard remains closed pending end-to-end qualification.
