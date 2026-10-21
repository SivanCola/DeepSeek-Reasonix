# History Search Catalog

Reasonix stores the history search projection at
`<cache root>/history-search/v1.sqlite`. Session JSONL, event logs, metadata,
sub-agent transcripts, and archives remain authoritative. Removing or
rebuilding the database never removes conversation data, and older Reasonix
versions continue to read the same authoritative files.

The catalog stores normalized retrieval tokens in FTS5, not complete message
text. English tokens retain the existing lowercase/code-symbol semantics and
CJK text retains overlapping bigrams. Snippets and `around` context are loaded
from the authoritative source only after SQLite has selected the final
candidates. User, assistant, tool input, tool error, and tool output parts are
indexed; ordinary tool output remains excluded by the default search kinds.

Successful session persistence sends a non-blocking, path-coalesced hint after
the authoritative commit and after file locks have been released. Continuous
appends read only the new display-index range. Rewrites, missed notifications,
external writers, or fingerprint mismatches rebuild that source in the
background. Only one full transcript decoder runs at once, checkpoints are
persisted per source, and one corrupt source cannot stop other histories.

The Agent `history` tool and Desktop history manager share the projection.
Search never starts a synchronous directory scan. While indexing is incomplete,
existing results return immediately with explicit progress. Runtime open,
running, and current state is overlaid from memory; SQLite cannot restore stale
runtime ownership. Provider-visible tool name, description, schema, defaults,
and ordering are unchanged.

The database uses the common disposable projection policy: local disks use WAL,
`synchronous=NORMAL`, foreign keys, private permissions, and a short busy
timeout. Remote or unavailable cache directories fall back to memory. Integrity
or migration failures quarantine and rebuild the cache; future schema versions
are preserved in degraded read mode.

Diagnostics and safe rebuild commands:

```sh
reasonix doctor catalogs [--json]
reasonix catalogs reindex history [--dir PATH ...] [--json]
```

Diagnostics never print queries, tokens, snippets, messages, tool arguments, or
provider content. A rebuild replaces only this disposable projection.

## Interactive loading / 交互加载

Search rebuilds and legacy listing replay share a process-local background
scheduler. Interactive session open/history reads preempt cancellable work;
lightweight tree refreshes only defer new work so progress notifications cannot
pause their own producer.

Scans retain their generation across yields, and only
completed scans perform missing-source cleanup. Separate processes have separate
schedulers, even when they share one SQLite projection.

搜索重建与旧版列表重放共享进程内后台调度器。打开会话和读取正文会抢占可取消的任务；
轻量列表刷新只推迟新任务，避免进度通知不断暂停自身。暂停不会完成扫描代次，也不会清理
尚未访问的来源；只有完整扫描结束才能处理缺失记录。不同进程即使共用 SQLite，也各自调度。

| Contract / 协议 | Behavior / 行为 |
| --- | --- |
| `GET /tree?progressive=1` | Returns discovered rows and `listingState`, `listingRevision`, `countsComplete`, `pendingMetadata`, `failedMetadata`; returns without foreign transcript replay. 返回已发现行及覆盖状态，不同步重放旧正文。 |
| `retryMetadata=1` | Retries failed metadata reads once on explicit retry. 显式重试时重新读取失败摘要。 |
| `GET /history/page` | Latest 200 raw messages by default; `limit` accepts 1–500. Returns absolute `start/end/total`, `snapshot`, `nextCursor`, and cross-page tool context. 默认最近 200 条原始消息，范围使用全局下标，附带快照、游标及跨页工具上下文。 |
| `cursor` | Bound to a session generation and immutable prefix length; append preserves it, rewrite/rebind refuses with `history.snapshot_changed`. 追加保留游标，重写或重新绑定使游标失效。 |
| ETag | Checks snapshot/range, display settings and selected attachment metadata before body projection. 正文转换前检查快照范围、显示设置和当前页附件元数据。 |

## Compatibility / 兼容性

| Format / 格式 | Existing reader / 旧读取方 | Current reader / 新读取方 |
| --- | --- | --- |
| Authoritative sessions / 权威会话数据 | Unchanged / 不变 | Full resume validation remains required / 仍执行完整恢复校验 |
| Legacy summary cache / 旧摘要缓存 | Unsupported cache versions are ignored and rebuilt / 忽略未知缓存版本并重建 | Format 2 verifies manifest identity and log signature; older cache formats are rebuilt / 格式 2 校验身份与日志签名，旧缓存重新生成 |
| `/tree` without opt-in / 未启用渐进模式 | Complete-list contract retained / 保留完整列表语义 | Uses the shared resolver / 使用共享摘要解析器 |
| `/history` | Existing full response retained / 保留全量响应 | Paging clients fall back only on explicit unsupported-route responses / 分页客户端仅在明确不支持接口时回退 |

Listing/search caches and in-memory page indexes are derived data. Their loss,
cancellation or rebuild never changes write leases, provider prompts, or the
authoritative conversation. A failed cache write retains reusable summaries in
memory and retries persistence with backoff.

列表与搜索缓存、内存分页索引均为派生数据。丢失、取消或重建不会改变写租约、模型提示词或
权威会话；缓存写入失败时保留内存摘要，并退避重试持久化。
