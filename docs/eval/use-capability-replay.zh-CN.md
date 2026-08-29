# use_capability 回放评测

这是 1.33.0 的配对评测步骤：比较缓存稳定的 `use_capability` 代理，以及把 MCP
工具展开进 provider 可见 schema 的基线。真实模型运行可选，且必须使用一次性
Reasonix home。

## 测什么

同一批五道任务各跑两次：

1. **代理（默认）：** 只用 `use_capability`。共享 Host + 磁盘 schema 缓存。
2. **基线：** 临时配置仍把 MCP 工具展开进 provider 请求（原生 Tool Search 保持关闭）。

记录 `tools/list` 次数、首 token 延迟、缓存命中 token。不要上传提示词、密钥或工作区路径。

## 步骤

1. 使用一次性 `REASONIX_HOME` / `REASONIX_CACHE_HOME`。
2. 选五道需要先发现再调用 MCP 的代表性任务。
3. 每道题先跑代理再跑基线。模型、effort、工作区保持一致。
4. 把五对结果写成与 `internal/eval/replay/testdata/paired_runs.json` 相同的 JSON。
5. 计算中位数：

```bash
go test ./internal/eval/replay/ -run TestMedianReportFivePairedRuns
```

夹具证明中位数计算。有凭据时用实跑数字替换。报告 `tools/list` 中位数差和延迟中位数差；
代理胜出表现为更少的远程 list（差值为负）。

原生 Tool Search 默认关闭，不纳入本评测。
