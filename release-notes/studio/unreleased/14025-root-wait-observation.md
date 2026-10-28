## 变更

- 为受跟踪命令增加根进程 Wait 完成信号，明确区分取消返回与等待真正完成；
  不改变取消策略，不把该信号当成整个进程范围结束的证明。Refs #14025
- Add a root-wait completion observation for tracked commands, separate from
  bounded cancellation return; it does not prove descendant completion. Refs #14025
