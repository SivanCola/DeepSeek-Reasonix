## 变更

- 为范围未知的写入增加显式事务原语，跨根路径及硬链接别名保守互斥，同时保留
  不相交具名写入与模型额度独立；尚未接入运行时或改变默认策略。Refs #14025
- Add explicit opaque-write admission across cooperating registry users while
  preserving disjoint named writes and independent model capacity. Runtime and
  default activation remain separate. Refs #14025
