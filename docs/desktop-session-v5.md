# Desktop Session v5

Desktop Session v5 makes `SessionRef{HostID: "local", SessionID}` the only
runtime identity for local conversations. Workspace membership is presentation
state, not a storage locator, and a tab is only a disposable view over that
identity.

## Authoritative state

- `desktop-sessions-v5/by-id/<session-id>` owns immutable headers, manifests,
  events, and content references.
- `desktop/workspace-state-v1.json` owns Workspace and Session ordering,
  visibility, archives, and in-flight create reservations.
- Canonical title, model, and turn metadata remain events. Query indexes are
  disposable projections and cannot add or remove Workspace members.
- Local Desktop uses one `SessionService` with host ID `local`. A Workspace
  path is recorded in a new session header but is never needed to open it.
- Workspace attachment validates the candidate SessionID against its immutable
  header `cwd`; the ordered registry is the membership authority, while the
  header prevents one Session from being attached to the wrong Workspace.

Creation and rotation reserve a SessionID in `pendingCreates`, seed and flush
the canonical session, attach the ID to its Workspace, and only then expose a
ready tab. Startup completes an interrupted attach when the session exists and
drops the reservation when it does not. Tab pruning is refused if snapshot,
metadata, or registry publication has not completed.

## Opening and recovery

The Workspace browser lists registry IDs even while their projections are
being rebuilt. `OpenSession` first reads history without a controller and never
creates a replacement for a missing or damaged ID. A navigation sequence fences
late results so only the newest selection can replace the visible runtime.
Frontend transcript, draft, operation, paging, and hydration fences use the
complete SessionRef first; legacy paths are only a compatibility fallback.

When a saved model no longer exists, Desktop retries the same SessionID with
the configured default model, appends the replacement `session/config` event,
and reports a non-blocking warning. History remains independent of projection
and controller startup.

## Upgrade boundary

The v5 migrator reads, but never modifies, canonical v4 and legacy JSONL data.
Imports are staged, validated, atomically published, and attached to a Workspace
before their ledger record becomes complete. Same-ID/same-content imports are
idempotent; same-ID/different-content imports receive a stable `migr-<hash>` ID.
Failures remain retryable and do not stop other sources from migrating.

Protocol 9 is a hard Desktop shell/host boundary. Older releases retain their
original data but do not see sessions created only in v5. Remote and Serve
session protocols are unchanged.

## 中文说明

Desktop Session v5 将 `SessionRef{HostID: "local", SessionID}` 设为本地会话
唯一运行时身份。Workspace 只管理展示归属与顺序，tab 只是可淘汰的视图，
Topic 和项目路径都不再参与打开 canonical 会话。

- `desktop-sessions-v5/by-id/<session-id>` 保存不可变 Header、Manifest、事件
  和内容引用；canonical 事件仍是标题、模型及轮次 metadata 的唯一真相。
- `desktop/workspace-state-v1.json` 保存 Workspace/Session 顺序、可见性、归档
  状态和新建事务；查询索引损坏或重建时不得删除其中的 SessionID。
- Registry attach 会用不可变 Header 的 `cwd` 校验 Workspace 归属；Registry
  仍是成员与顺序真相，但错误 Workspace 不能收录该 SessionID。
- 新建与轮换必须依次完成 pending 预留、canonical seed/flush、Registry attach，
  然后才允许 tab 进入 Ready；淘汰 tab 前会重新验证持久化结果。
- 打开会话先进行与 controller 无关的历史读取，缺失或损坏的 ID 不会生成
  空白替代会话；navigation sequence 保证快速连续点击仅最后一次生效。
- 前端 transcript、草稿、操作、分页与 hydration 防线优先比较完整
  SessionRef；legacy path 只作为旧会话兼容回退。
- 原模型失效时，在同一 SessionID 上使用 Desktop 默认模型恢复，并追加新的
  `session/config` 事件；不改变已有历史和 provider-visible prompt/tool bytes。

v5 迁移器只读保留 canonical v4 与 legacy JSONL。每个导入都先在临时目录完成
校验和原子发布，再写入 Workspace；同 ID 同内容幂等复用，同 ID 不同内容稳定
映射为 `migr-<hash>`。Protocol 9 是 Desktop shell/host 的硬边界；远端与 Serve
协议保持不变，旧版只能看到升级前保留的数据。
