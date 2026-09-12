# Harness 机制改造验收报告

验收基线：Reasonix `main-v2@986a6bc967`；参考行为：DeepSeek Harness `master@c291e7961a`。验收日期：2026-09-13。

## 结论

新机制已默认启用，没有新旧执行语义开关。结构化文件修改由宿主管理的实时文件观察和版本检查保护；读取范围、证明回执、操作结算、Auto Guard 和未知副作用恢复状态不再参与工具准入或完成判断。旧字段和旧恢复终态只用于兼容读取与历史展示。

## 七项问题行为

| 问题 | 验收操作 | 结果 | 覆盖层 |
| --- | --- | --- | --- |
| #9994 | `read → edit → bash → edit` 连续执行三轮 | 通过；成功编辑刷新观察，bash 不消耗或冻结文件观察 | Agent 集成、文件工具、macOS、Windows 11 |
| #9995 | 编辑后执行只读 git/bash，再次编辑 | 通过；命令不建立文件证据债务 | Agent 集成、macOS、Windows 11 |
| #10067 | 中文路径和 CRLF 文件读改跑再改 | 通过；路径和编码路由保持一致 | 文件工具、macOS、Windows 11 |
| #10103 | 文件移动和删除穿插在连续编辑流程 | 通过；移动不覆盖目标，成功后更新源/目标观察 | 文件工具、Agent 集成 |
| #10053 | 大文件只读一个窗口后运行命令并结束 | 通过；没有全文债务、强制补读或 final gate | Agent 集成、窗口读取 |
| #10085 | 同批 `read → edit → edit → bash` | 通过；按实际执行顺序同步更新观察 | Agent 批次集成、macOS、Windows 11 |
| #10153 | 模拟副作用已经发生但结果丢失，随后检查状态并继续调用 | 通过；记录 `unknown`，终态为普通 `interrupted`，不要求用户确认、不禁用网络或相同调用，也不自动重放 | 控制器崩溃测试、turn ledger、Desktop 兼容 API |

## 文件新鲜度与并发

- 未观察覆盖返回 `FS_NOT_OBSERVED`；确认不存在时只允许不可覆盖的原子创建。
- 任意成功文本窗口可观察当前版本；连续成功写入直接推进版本。
- 同大小改写、恢复 mtime、权限变化、文件替换和别名访问会使旧观察失效。
- 两个写者基于同一版本竞争时只有一个能提交；并发创建不会覆盖先创建者。
- 本地写入保留临时文件、原子替换、权限和编码；缓冲区与磁盘使用不同目标身份。
- `edit_file`、`write_file`、`multi_edit`、`notebook_edit`、`delete_range`、`delete_symbol`、`move_file` 和 `read_file` 已接入统一观察状态或提交后状态更新。

## 中断、完成与兼容

- 工具进入主体前持久化启动事实；取消后已启动调用收束，未启动调用获得明确结果。
- 重启发现缺少可靠结果时补全一次 `unknown` 工具结果；当前运行不生成 `recovery_required` 或 `requires_user_decision`。旧值仍可解码和只读显示。
- 恢复上下文只提供一次有界事实和检查建议；文件当前状态不会被当作旧调用成功证明。
- `complete_step`、`review_report` 和读取策略回执不再发现；旧调用返回普通 `tool_retired`。
- 恢复动作端点返回 `tool_recovery_retired`，不会确认、拒绝、检查隐藏参数或重放操作。
- `todo_write` 只校验字段、状态、层级和稳定 ID；完成状态由模型显式更新。
- 连续重复调用仅在第 3、5、8 次提醒，不拒绝调用。

## 平台与产品验证

| 环境 | 验证 | 结果 |
| --- | --- | --- |
| macOS | 根模块完整 Go 测试与 vet；Desktop、SDK 独立 Go 模块；契约、golden、缓存和仓库静态检查 | 通过 |
| Desktop 前端 | 完整前端测试和生产构建 | 通过 |
| Desktop 壳 | shell 测试 206 项；Electron 布局场景 57 项 | 通过 |
| Windows 11 虚拟机 | 原生 volume serial/file index/handle identity；同大小变化；真实 ACL `ChangeTime`；文件观察、并发创建/编辑和七项 harness 场景 | 通过 |
| Desktop 历史兼容 | 旧恢复卡只读、无动作按钮；当前工具卡保留未执行/失败/中断/未知事实；继续输入不受阻 | 通过 |

Windows 验证在本机 Parallels Windows 11 中原生执行，不以交叉编译代替。测试副本和临时产物已清理。

## 删除与保留边界

已删除全文读取债务、批次证据冻结、source-token 授权、锚点阅读范围影子状态、操作 prepared/applied/settled 状态机、完成证明门禁、Auto Guard reviewer、恢复确认动作、重复/无进展拒绝器，以及其不再被调用的 shell 证明预检代码。

保留 Goal、Plan 审批、普通权限策略、沙箱、检查点、多代理、工具调用配对和执行事实展示。旧 provider/session 字段、`recovery_required` 枚举及前端识别只承担旧历史兼容，当前运行路径不写入或激活它们。

## 保证范围

窗口读取只表示观察过该版本，不表示全文审阅。bash、MCP 和外部程序不会授予文件观察；它们造成的文件变化由下一次结构化修改发现。ACP 缺少原子条件写接口，本地发布前检查也无法约束不遵守进程内锁的外部写者，因此不承诺通用跨进程 CAS。未知外部副作用没有宿主级 exactly-once 保证。
