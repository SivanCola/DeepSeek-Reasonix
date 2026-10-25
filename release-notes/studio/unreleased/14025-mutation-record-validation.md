## 修复

- 写入事务原语遇到未知注册表字段或无法理解的路径影响类型时，拒绝新准入并保留
  记录；已知旧记录仍可回收。本次不启用新的共享写入模式。Refs #14025
- Mutation admission preserves unfamiliar records and refuses new requests.
  Known legacy records remain reclaimable; no new shared-write mode. Refs #14025
