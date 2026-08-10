# Windows 10 LTSC 2019 崩溃调查运行手册

<a href="./WINDOWS_LTSC_2019_CRASH_RUNBOOK.md">English</a>

本手册是 Windows build `17763` 的发布与证据检查清单。在满足下述证据门槛前，
问题必须保持未关闭；仅发布诊断版本不代表问题已经解决。

## 发布顺序

1. 从 `fix/windows-ltsc-crash-diagnostics` 冻结一个候选提交，并在实验记录表中
   记录其完整 SHA。
2. 备份 `reasonix-crash` D1 数据库。
3. 应用 `workers/crash-report/migrate-diagnostics-v2.sql`，验证
   `report_daily`、`report_installations`、
   `report_installations_fingerprint_date` 索引以及全部新增列。
4. 部署 crash-report Worker。分别使用旧 Report/Ping/Metrics payload 和
   `channel=test` 的 diagnostics-v2 payload 做 smoke，确认后者进入开发
   fingerprint 命名空间且不出现在 Stable 排行中。
5. 使用冻结 SHA 构建已签名 Windows amd64 包。不得移动或重建已发布标签。
6. 完成下述 LTSC 矩阵后，才可发布 Desktop 大版本。
7. 在 Diagnostics 中保留审计轨迹，并一次性执行以下状态整理：
   - 忽略 `[go panic] safe` / `v9.9.9`，备注：`单元测试误上传；已通过拒绝联网的
     测试端点修复`。
   - 将 `72daba81` 标记为 resolved，并设置
     `resolved_in=desktop-v1.19.3`。
   - 忽略旧 `desktop.abnormal_exit` 重放分组，备注：`legacy startup-state
     重放污染；已由 lifecycle v2 替代`。

## 兼容性 smoke payload

旧 payload 缺少所有 v2 字段。新的 Windows payload 使用启动 ping 已有的同一个
32 位匿名安装 ID，并附加 `osBuild`、`osRevision` 和受限的 `webview2` 对象。
测试报告必须使用 `channel=test`，smoke 不得使用看似正式的 Stable 版本号。

smoke 后验证：

- 渲染 HTML、审计记录、日志和导出样本中均不存在原始安装 ID；
- 报告样本只包含 WebView2 模块文件名，不包含完整路径；
- 重复上报时，identified event 和 installation event 计数都正确累加；
- 删除测试分组时，同时删除其 `report_daily` 和 `report_installations` 记录；
- 30 天保留任务能以受限分块清理两张聚合表。

## LTSC 对照矩阵

所有实验单元使用同一个已签名候选 SHA。在实验室记录表中本地记录 OS revision、
WebView2 Runtime、GPU 型号和驱动版本；客户端不会自动采集显卡驱动信息。

| 系统 | Runtime | GPU | 安装路径 | 必测负载 |
| --- | --- | --- | --- | --- |
| Windows 10 LTSC 2019 `17763` VM | 用户版本 + 最新 Evergreen | 开/关 | 全新安装 + v1.18/v1.19 升级 | 20 次冷启动/退出、10 次更新重启、60 分钟工作负载、50 次最小化/恢复 |
| Windows 10 LTSC 2019 实体 GPU 设备 | 用户版本 + 最新 Evergreen | 开/关 | 全新安装 + 可行时执行升级 | 同上，另加 DPI/多屏及 RDP 连接/断开 |
| Windows 10 22H2 `19045` | 最新 Evergreen | 开/关 | 全新安装 | 相同对照负载 |
| Windows 11 `22631` 或当前 `26200` | 最新 Evergreen | 开/关 | 全新安装 | 相同对照负载 |

GPU-off 组设置 `REASONIX_DISABLE_WEBVIEW2_GPU=1`，GPU-on 组设置为 `=0`。
记录 WER 1000/1001、可靠性监视器时间和对应 Diagnostics fingerprint。dump 仅可在
用户明确授权后通过私密渠道传输，并在分析完成后删除。

## 根因证据门槛

仅在满足以下任一条件时确认根因：

- 至少两台 LTSC 节点可复现，而对照系统不复现；或
- 至少三个不同的 `17763` 安装命中同一 fingerprint，活跃 LTSC 安装不少于
  30 个，且 `17763` 影响率至少是 `19045` 的三倍。

仅当每台 LTSC 设备的 GPU-on 复现不少于 `2/20`，而两台设备 GPU-off 合计
`0/40` 且每台两小时测试均为零故障时，才可对 build 17763 默认禁用 GPU，并保留
环境变量的反向覆盖能力。

- `integrity_failure`：调查签名、注入 DLL、策略和安全软件，不应用 GPU workaround。
- `out_of_memory`：调查内存与会话资源压力。
- Runtime 版本聚集：增加最低 Runtime 检查和原地更新提示。
- 只有 renderer 故障且 `reload_succeeded` 时，视为已恢复的 WebView2 事件，
  不视为 Reasonix 应用崩溃。
- lifecycle-v2 异常退出若没有原生原因，必须取得对应 WER 或经授权的 dump 后才能
  结案。

## 生产环境七天观察

连续检查七个完整 UTC 日：

- diagnostics-v2 身份覆盖率至少为 95%；低于 90% 时不得给出精确影响率；
- legacy replay fingerprint 中不得出现新版本样本；
- browser fatal、renderer recovered/recovery-failed、退化子进程和通用
  lifecycle-v2 总量能够形成一致解释；
- 对比 `17763` 与 `19045` 的受影响安装率，并检查新增 fingerprint；
- 确认保留任务和 ingest sentinel 持续健康。

若生产数据揭示产品根因，发布只包含该修复和回归测试的最小补丁。LTSC 实验室必须
达到 `0/40` 复现，再继续观察七天。若首个窗口后证据仍不足，则扩展观察到 30 天，
并明确保持问题未关闭。
