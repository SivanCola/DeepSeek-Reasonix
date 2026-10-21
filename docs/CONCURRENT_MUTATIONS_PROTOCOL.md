---
owner: @SivanCola
backup: @esengine
status: active
reviewed: 2026-10-22
---

# Concurrent mutation protocol compatibility / 并发变更协议兼容

This is an implementation and qualification record. It does not declare the
complete concurrency plan accepted or enable a new installation default.
See [the user and implementation guide](CONCURRENT_MUTATIONS.md).

本文记录实现及验收边界，不代表完整并发方案已验收，也不启用新安装默认策略。
操作方式见[使用与实现说明](CONCURRENT_MUTATIONS.md)。

## 中文

| 数据或协议 | 旧数据及新读写器 | 旧读写器 | 结论 |
| --- | --- | --- | --- |
| 未配置 collaboration | 沿用兼容模式；不重写旧开关 | 继续使用旧设置 | 不自动迁移活动运行 |
| auto_integrate | 未配置视为开启；用户配置可关闭；保存值和有效值分开 | 不认识该选项 | 新运行时生效；旧程序不能据此提供自动整合保障 |
| mutation 资源记录 | 校验版本、身份、路径及 OS 所有权；未知/损坏记录保留并拒绝绕过 | 旧 workspace claim 保持互斥 | 兼容长锁仍可能阻塞短事务 |
| 无版本 iso_ 结果 | 保留身份并重新观察候选 | 仍可能使用旧清理规则 | 不能把缺失的旧证据视为通过 |
| v2 iso_ 结果 | 独立 marker、封存清单、保留未知字段 | 仅支持旧格式的程序拒绝该版本 | 候选变化需恢复；终态记录保留 |
| v3 根导入结果 | 记录基线、不可变根快照、依赖副本和来源身份 | 已发布的 v2 reader 拒绝 v3 | 活动根现场不会成为可删除候选 |
| publication revocation v1 | 同一 entry 锁内撤销、应用及提交；先持久撤销再更新根记录 | 不支持撤销的早期实验 v3 reader 可忽略 sidecar | 早期实验 v3 不属于安全降级范围 |
| int_ 尝试 v1 | 独立候选及精确快照检查；恢复保持成果身份 | 旧程序不读取该目录 | 历史验证不代表当前工作区仍有效 |
| 根环境 v1 | 可重开现场；无冻结检查契约时不从现场补取检查 | 实验 v1 reader 不能理解新的安全规则 | 不授予自动验证发布资格 |
| 根环境 v2 | 检查契约在可写前冻结；fork 继承；版本及契约不可替换 | v1 reader 拒绝 v2 | 未变化的未知契约字段保留 |
| publication v1 | preparing、pending、acknowledged；只推进导出的根游标 | 未知格式保留 | 新 ID 替换旧对象；同 ID 才递归保留未知字段 |
| 环境绑定 v1 + 会话事件 envelope v6 | 首条日志持久绑定；sidecar 缺失从日志恢复，冲突拒绝；绑定不改变原始消息日志、revision 或摘要 | 已验证的前版 reader/writer 将 schema 6 作为未来格式拒绝；不能忽略日志回落 checkpoint | 追加、压缩及修复保留屏障；旧 sidecar-only 实验会话须由当前会话 authority 在执行前升级 |
| 检查进程崩溃 | 取得 run 锁后核验封存内容，同 ResultID 重检 | 不共享过程内受控凭据 | 重启后证据保持未知，不能反序列化为有效 |
| 终态收据 | 清理后重复同操作幂等；相反操作拒绝 | 按其理解的格式读取 | 应用成功和当前验证有效是两个事实 |
| 等待事件 | 可选事务、会话、执行及原因字段 | 旧事件继续读取 | 无身份的结束事件仅闭合无身份的旧等待 |
| 未完成源整合日志 | 当前程序所有策略均阻止普通写入；覆盖最终写根及重建复用的旧 owner；按预览条件恢复 | 不理解日志的旧程序仍可能忽略它；独立旧会话不受新会话 envelope 保护 | 当前程序不能切换 legacy 绕过；不能据此承诺任意旧程序的降级写入安全 |

验证平台及性能报告必须绑定具体源码快照。源码审计、受影响包测试、原生平台测试、
浏览器模拟界面、托管 CI 和发布验收是不同证据，不能互相替代。

环境绑定先在会话保存锁内以严格原子替换发布 v6 envelope，再写派生 sidecar。
Clear、Fork 和恢复副本均在消息发布前绑定目标身份；恢复使用保存 owner 发出的
限时 publication capability，不能再次取得同一保存锁。

历史版本保留绑定并标记
`archive_only`，允许阅读但拒绝执行。移动、原生导出及跨项目采用尚不支持迁移绑定，
因此明确拒绝，不能产生可执行的无约束副本。物理重命名同样拒绝。

这些边界约束支持协议的读写器。旧程序或任意外部进程完全不读取会话/整合协议时，
新程序不能在崩溃后凭一个 sidecar 阻止其直接写文件；这种降级不属于安全兼容范围。

Linux 的显式离线验证运行时可以产生过程内所有的受控凭据。macOS、Windows 目前
不支持该受控验证器；普通 Shell 观察不会被升级为完整输入证明。非执行位或目录
权限变更无法由当前 Git 应用协议重现时，会拒绝发布并保留成果。

## English

| Format or protocol | Existing data and current reader/writer | Previous reader/writer | Conclusion |
| --- | --- | --- | --- |
| Missing collaboration | Preserve compatibility mode and old switches | Keeps its settings | No migration during active execution |
| auto_integrate | Missing means enabled; user-only choice; saved/effective values separate | Ignores the unknown setting | Applies to new runtimes; does not establish old-program integration guarantees |
| Mutation resources | Validate version, identity, paths and OS ownership; retain unsupported records | Exclusion through legacy workspace claims | Legacy long holds can still block short transactions |
| Unversioned iso_ | Retain identity and observe current candidate | May retain old cleanup behavior | Missing evidence is unknown |
| v2 iso_ | Separate marker, sealed manifest, unknown-field preservation | Older-format-only readers refuse | Content drift requires recovery; terminal records remain |
| v3 root imports | Baseline, immutable root snapshot, frozen dependencies and origin | Published v2 readers refuse v3 | Persistent live scenes never become disposable candidates |
| Revocation v1 | Revoke, Apply and Commit fence at the same entry lock; revocation persists first | Early experimental v3 readers can ignore the sidecar | Those experimental readers are outside the safe downgrade scope |
| int_ v1 | Separate candidate and snapshot-bound checks; recover same identity | Does not read this directory | Historical verification does not prove current source validity |
| Root environment v1 | Reopen retained scene; never refill missing contracts from mutable instructions | Experimental v1 lacks new guarantees | No automatic verification publication authority |
| Root environment v2 | Host contract frozen before writable execution; inherited by fork; immutable version/contract | v1 readers refuse v2 | Unchanged opaque contract fields retained |
| Publication v1 | preparing/pending/acknowledged; cursor advances only to root export | Unsupported formats retained | New identities replace old objects; recursive preservation only within one ID |
| Environment binding v1 + session-event envelope v6 | Durable first-record binding; recover absent sidecar from WAL, refuse conflicts; preserve original message WAL, revision and digest | Qualified previous reader/writer refuses schema 6 as a future format, without checkpoint fallback | Append, compact and repair retain the fence; live session authority upgrades experimental sidecar-only sessions before execution |
| Check-process crash | Acquire run owner, verify sealed content and recheck same ResultID | No reusable process-owned receipts | Restart leaves verification unknown |
| Terminal receipts | Same-action retries idempotent after cleanup; opposite action refuses | Reads supported formats | Publication and current verification validity remain separate facts |
| Wait events | Optional transaction/session/execution identity and reason | Legacy events remain readable | Identity-free closure matches only identity-free waits |
| Source integration journals | Every strategy in the current program blocks ordinary writes; cover final write roots and reused owners after rebuild; preview-bound recovery | Unaware older programs can ignore journals; independent old sessions have no new session envelope | Switching the current program to legacy cannot bypass recovery; arbitrary old-program downgrade writing is outside this guarantee |

Bind platform and performance reports to a specific source snapshot. Source
review, owning-package tests, native platform execution, mocked browser flows,
hosted CI and release qualification are distinct evidence classes.

Binding publishes the v6 envelope with strict atomic replacement under the
session save lock before creating the derived sidecar. Clear, Fork and recovery
bind target identity before publishing messages.

Recovery reuses a scoped
publication capability from the save owner and never reacquires the same lock.

Historical copies retain their binding with `archive_only`: readable, never
executable. Move, native export, cross-project adoption and physical rename
refuse bound sessions until they support constraint-preserving migration.

These boundaries govern participating readers and writers. An unaware old
program or external process can ignore every session and integration protocol;
a sidecar cannot prevent its direct file writes after a crash. Such downgrades
are outside safe compatibility.

The explicit Linux offline runtime can issue process-owned controlled receipts.
macOS/Windows do not currently implement this verifier; ordinary shell
observations remain unknown. Permission changes outside the Git apply protocol's
representation refuse publication and retain the result.
