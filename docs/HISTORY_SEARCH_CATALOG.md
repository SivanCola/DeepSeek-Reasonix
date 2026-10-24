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

Successful persistence sends a non-blocking, path-coalesced hint after commit
and after file locks have been released. Continuous appends read only the new
display-index range.

Rewrites, missed notifications, external writers, or fingerprint mismatches
rebuild that source in the background. The catalog worker replays one source
at a time; foreground loads can run concurrently.

Checkpoints are persisted per source. Returned replay failures are isolated
so other histories remain searchable.

Legacy 1.x DAG replay retains message locations rather than all historical
bodies, then loads the effective selected transcript. Rewinds, patches,
redactions, and fork-specific system messages keep their original semantics.
Message preflight scans JSON without allocating decoded copies of large strings.

The encoded-log limit remains 512 MiB. Additional safety budgets are:

- One encoded record: 64 MiB.
- Aggregate retained message payloads: 64 MiB.
- Legacy DAG metadata: 64 MiB.

Payload accounting uses encoded JSON bytes, not a process-memory quota.
Decoder overhead and concurrent foreground work consume additional memory.

Exceeding a budget returns `SessionReplayLimitError`, leaves source files
unchanged, and never falls back to an older checkpoint. The catalog records
that source as failed while healthy sources remain indexable.

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

Scans retain their generation across yields, and only completed scans perform
missing-source cleanup. Separate processes have separate schedulers, even when
they share one SQLite projection.

搜索重建与旧版列表重放共享进程内后台调度器。打开会话和读取正文会抢占可取消的任务；
轻量列表刷新只推迟新任务，避免进度通知不断暂停自身。暂停不会完成扫描代次，也不会清理
尚未访问的来源；只有完整扫描结束才能处理缺失记录。不同进程即使共用 SQLite，也各自调度。

| Contract / 协议 | Behavior / 行为 |
| --- | --- |
| `GET /tree?progressive=1` | Returns discovered rows and `listingState`, `listingRevision`, `countsComplete`, `pendingMetadata`, `failedMetadata`; returns without foreign transcript replay. 返回已发现行及覆盖状态，不同步重放旧正文。 |
| `retryMetadata=1` | Retries failed metadata reads once on explicit retry. 显式重试时重新读取失败摘要。 |

## Compatibility / 兼容性

| Format / 格式 | Existing reader / 旧读取方 | Current reader / 新读取方 |
| --- | --- | --- |
| Authoritative sessions / 权威会话数据 | Unchanged / 不变 | Full resume validation remains required / 仍执行完整恢复校验 |
| Legacy summary cache / 旧摘要缓存 | Unsupported cache versions are ignored and rebuilt / 忽略未知缓存版本并重建 | Format 2 verifies manifest identity and log signature; older cache formats are rebuilt / 格式 2 校验身份与日志签名，旧缓存重新生成 |
| `/tree` without opt-in / 未启用渐进模式 | Complete-list contract retained / 保留完整列表语义 | Uses the shared resolver / 使用共享摘要解析器 |

Listing/search caches are derived data. Their loss, cancellation or rebuild never
changes write leases, provider prompts, or the authoritative conversation. A
failed cache write retains reusable summaries in memory and retries persistence
with backoff.

列表与搜索缓存均为派生数据。丢失、取消或重建不会改变写租约、模型提示词或权威会话；
缓存写入失败时保留内存摘要，并退避重试持久化。
