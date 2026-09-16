# Archive integration qualification / 归档集成验收

Base: `main-v2@a4b83056a99ff1da1700a585e8b9da71fb872838`.
Desktop protocol 10; registry schema 2; canonical codec and Serve unchanged.
The earlier archive-v2.5 build is not evidence for this integration.

基线如上，本地协议 10、注册表 schema 2，正文 codec 及 Serve 不变。
旧 archive-v2.5 测试包不作为本次集成的验证证据。

## Current evidence / 当前证据

All data tests use disposable fixtures, not personal history. These counts do
not claim that a user's existing data has already been repaired.
全部数据测试使用一次性夹具，不操作个人历史，不宣称已经修复用户的真实数据。

| Gate / 门禁 | Status / 状态 |
| --- | --- |
| Root Go full suite / 根模块全量 | All packages except stale generated inventory passed; regenerated inventory owning tests passed / 除生成物过期外通过，重新生成后 inventory 测试通过 |
| Desktop full suite / Desktop 全量 | One source-error diagnostic regression found and fixed; focused reproduction passes, final full run pending / 发现并修复一项来源错误诊断回归，定向通过，最终全量待执行 |
| Frontend / 前端 | 379 suites passed; production build and typecheck passed / 379 套件及生产构建、类型检查通过 |
| Race / 竞态 | Focused desktop registry/runtime and session purge race checks passed before final review fixes; rerun pending / 定向检测通过，评审修复后待复核 |
| Fault tests / 故障测试 | Content-publication interruption, receipt replay, pending purge visibility, external writer exclusion, symlink/staging refusal / 正文发布中断、回执重放、删除未完成入口、外部写锁、符号链接及暂存冲突 |
| Production package / 生产包 | New integrated package and real UI restart cycle pending / 新集成包及真实界面重启闭环待验证 |
| Windows/Linux / 跨平台 | Native runs unavailable on this macOS host; no native success claim / 本机未原生运行，不标记为通过 |

## Review repairs / 评审修复

- Preserve upstream multi-head migration and durable submission receipts;
  quarantine changed adopted sources without overwriting continued targets.
- Stamp command generation while holding the registry writer lock and reject
  intervening lifecycle changes again at child-operation admission.
- Publish proven legacy trash through an explicit archive-import commit;
  preserve its recorded timestamp or leave time unknown.
- Retain originals in the legacy purge RPC; canonical tombstones suppress
  repeated adoption. Reject unowned purge staging directories.
- Report malformed source manifests in the ledger while migrating healthy
  sources; replay failure does not suppress independent discovery.

- 保留上游多 head 迁移与持久提交回执；已采用来源变化进入待校验，不覆盖后续正文。
- 注册表写锁内写入准确 generation，子事务再次拒绝过期生命周期意图。
- 明确的旧回收站记录使用专用归档导入提交，保留已有时间或显示未知。
- 旧 purge RPC 同样保留原件；canonical 墓碑阻止再次采用，拒绝无归属的删除暂存。
- 损坏 manifest 记录失败且不影响健康来源，重放失败不阻断独立发现。

## Remaining qualification / 尚待验收

No release qualification is claimed until the integrated package, current-head
CI and final owner checks are recorded. Large-history list latency, real disk
exhaustion/power loss and native Windows/Linux locks remain separate gates;
cross-compilation and simulated failures are not substitutes.

集成包、当前提交 CI 和最终模块检查记录前，不宣称达到发布条件。大规模列表延迟、
真实磁盘满/断电、Windows/Linux 原生文件锁需单独验证；交叉编译或模拟失败不能替代。

See [compatibility guide](session-archive-upgrade.md) for source retention,
rollback and format handling. 降级、原件保留与格式矩阵见兼容文档。
