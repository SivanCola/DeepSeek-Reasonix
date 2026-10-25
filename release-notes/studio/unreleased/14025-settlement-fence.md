## 修复

- 普通隔离结果操作在持有结果租约后检查外部整合屏障，拒绝覆盖未知恢复状态；
  本批只增加读取方保护，不创建日志或完成恢复。Refs #14025
- Ordinary isolated-result operations refuse external settlement fences under
  their OS owner. This adds no journal writer or complete recovery. Refs #14025
