# 会话操作与运行时解耦

本文记录 2026 年 9 月引入的本地会话显式目标操作及其兼容契约。

## 路由规则

持久化会话操作按以下顺序解析 `SessionSelector`：

1. canonical `SessionRef`；
2. 经过校验的 `sessionPath`；
3. `topicId`。

高优先级字段无效时直接报错，不回退到低优先级字段。canonical 身份为
`hostId + sessionId`；legacy 身份为规范化且经过目录校验的路径，并结合操作
观察到的 BranchMeta/文件代次。运行时 binding 只是可选投影，不能用于判断
持久化会话是否存在。

标题、历史、搜索、归档、恢复、移动、删除以及 canonical Fork 均可在不选择、
不启动目标对话 controller 的情况下执行。发送、停止和实时模型控制仍要求
存在已打开且就绪的运行时 binding。

## 标题并发协议

canonical 会话继续使用原有 `session/title` 事件 payload，不改变持久化格式。
`Projection.TitleSequence` 由最后一条标题事件的序号重建，作为仅标题维度的
CAS token。普通消息追加不会制造无关标题冲突；每次显式保存标题（包括同名
保存）都会追加标题事件。

legacy BranchMeta 增加一个可选字段：

```json
{"title_revision":"不透明随机 mutation token"}
```

旧 sidecar 缺少该字段时仍可读取。首次创建标题快照时会在跨进程元数据锁内
初始化 token；手动和 AI 写入都会生成新 token。正文保存和列表投影写入保留
最新标题与 token；兼容的整记录 `SaveBranchMeta` API 仍允许调用方显式写入
标题。

AI 标题只有在标题 token、持久化目标身份和生命周期代次均未变化时才能提交。
取消请求只是资源优化；即使 provider 忽略取消并迟到返回，最终仍由 CAS
决定是否接收结果。

### 混合版本限制

遵守新协议的写入者可以检测 A→B→A 和同名保存。旧程序若删除
`title_revision`，新版会拒绝旧 AI 结果。但如果旧程序修改标题 A→B→A 的
同时刻意保留它不理解的旧 revision token，则任何实现都无法保证识别全部
此类混合版本写入；不得宣称跨任意旧版本完全消除 ABA。

## 辅助 Provider

冷会话 AI 标题通过独立 provider-only handle 执行，并使用目标会话及目标
workspace 上下文。配置型模型不会启动扩展运行时；扩展模型只启动其
`plugin/<name>` 所属包以及通过 capability requirement 声明的依赖。
辅助 handle 不会把工具、MCP、UI action 或 prompt contribution 发布到其他
对话，并在操作结束时释放自身 sidecar。

标题 prompt、最多三条用户消息及其长度限制保持不变，不改变 provider 可见
输入和主对话缓存前缀。

## RPC 与事件

版本在 Desktop RPC 边界以字符串传输。业务错误在 JSON-RPC error data 中
增量提供 `sessionCode`、`targetKey`、`operationId` 和 `retryable`。未分类
底层错误只显示统一产品提示，本地路径、controller、lease、provider 原始
响应和凭据不得进入 toast。

持久化成功后发送目标级增量提示：

- `session_metadata_changed`
- `session_archived`
- `session_restored`
- `session_moved`
- `session_deleted`

兼容期继续发送既有项目树通知。若增量事件丢失或无法排序，客户端应只重读
对应目标的权威元数据，持久化存储始终是最终权威。

