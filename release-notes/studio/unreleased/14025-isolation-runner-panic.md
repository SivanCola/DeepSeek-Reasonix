## 修复

- Preserve isolated-task work and its ID after a host runner panic, without
  automatically inspecting or deleting the candidate. Refs #14025
- Failed recovery publication explicitly reports the live-Store-only retry
  limit. Refs #14025
- 隔离任务的宿主 runner panic 后保留成果与恢复 ID，包括子任务已写文件的情况。
  使用既有待检查身份，不自动检查或删除候选；发布失败时明确仅当前 Store 可重试。Refs #14025
