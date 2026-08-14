# 会话所有权、回溯与 worktree 回退

<a href="./SESSION_OWNERSHIP.md">English</a>

Reasonix 如何决定谁可以写会话、冲突如何落盘，以及回溯和工作区隔离如何配合。

## 会话写者

同一会话文件同一时刻只有一个跨进程写者。票据是 session lease（`.lease.lock`）。
生产路径上的 Controller 绑定带 generation 的 `SessionWriter`；重新绑定会使旧
generation 立即失效。

已经持有 `SessionWriter` 的保存不再获取遗留的 `.jsonl.lock`。未绑定的测试 /
导入路径仍使用该兼容 flock。

事件日志（`.events.jsonl`）是权威来源。绑定写者的保存对日志尾部（size +
index 的 revision/digest）做 CAS，并用配对的内存 transcript 视图判断
no-op / append / replace。`.jsonl` 仍是兼容投影。

## 冲突

1. 事件日志尾部仍匹配当前写者 → 正常保存。
2. 磁盘已经覆盖本地前缀 → 采用磁盘版本，不建分支。
3. 真正分歧、日志被替换或原会话被删除 → 写入一条由根 branch ID + writer
   generation 决定的稳定 recovery 文件。后续冲突更新同一路径，不再嵌套。

## 回溯

- **代码**：恢复 before-image。当前已等于 before 的文件跳过；外部修改拒绝覆盖。
- **对话**：创建新会话分支。父会话 transcript 永不截断。
- **两者**：先分叉，再恢复文件。文件冲突时保留新分支并返回 `partial=true`。

新 checkpoint 写入 `turns/<turn>/meta.json` 和原始字节
`files/NNNN.before`（schema v3）。旧的 v1/v2 `turn-N.json` 仍可读。

结构化写工具在发布前重新校验存在性、SHA-256 和 mode，不匹配则返回
`ErrFileChanged`。

## Worktree 回退

Delivery worktree 仍是可选能力。非隔离目录使用 workspace lease（`filelock`）。
冲突卡片可以推荐已有 worktree。Git 不是运行前提；未安装 Git 的 Windows 仍通过
workspace lease 串行写入。
