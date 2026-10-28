子任务恢复拒绝无法识别的持久化状态。

## 修复

- 续跑、复制及祖先副本复用仅接纳已支持的 completed/cancelled 状态；缺失、空值或未知状态保留原记录并拒绝准备执行。关联 #14025。
- 已有身份与工具范围校验保持，不新增失败任务恢复或持久化格式。关联 #14025。

---

<!-- lang:en -->

## Fixed

- Subagent continuation, copying and ancestor-copy reuse admit only supported completed/cancelled states; missing, empty and unknown states preserve stored records and refuse preparation. Refs #14025.
- Existing identity and tool-scope checks remain; this does not add failed-task recovery or a persisted format. Refs #14025.
